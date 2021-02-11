package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/pkg/errors"
	"github.com/ryanc414/gas-tracker/prices"
)

const (
	baseURL         = "https://api.etherscan.io/api"
	maxNumGasPrices = 7 * 24 // 7 days of data, assuming run once per hour.
	tableName       = "gasPrices"
)

func main() {
	lambda.Start(HandleRequest)
}

func HandleRequest(ctx context.Context, _ struct{}) (string, error) {
	if err := run(ctx); err != nil {
		return "error", err
	}

	return "finished", nil
}

func run(ctx context.Context) error {
	apiKey := os.Getenv("ETHERSCAN_API_KEY")
	if apiKey == "" {
		return errors.New("ETHERSCAN_API_KEY is not set")
	}

	notifier, err := newEmailNotifier()
	if err != nil {
		return errors.Wrap(err, "while constructing email notifier")
	}

	var client http.Client
	gas, err := getMediumGas(ctx, &client, apiKey)
	if err != nil {
		return errors.Wrap(err, "while getting current gas price")
	}
	log.Print("medium gas is ", gas)

	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))

	// Create DynamoDB client
	svc := dynamodb.New(sess)

	gasPrices, err := readGas(svc)
	if err != nil {
		return errors.Wrap(err, "while reading gas prices from file")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	stats, err := getPriceStats(gasPrices)
	if err != nil {
		return errors.Wrap(err, "while calcuating gas price stats")
	}
	log.Printf("mean price = %v, stddev = %v", stats.Mean, stats.Stddev)

	category := prices.CategorisePrice(gas, stats)
	log.Print("the price now is ", category)

	lastCategory := getLastCategory(gasPrices)
	if category != prices.Average && lastCategory != nil && category != *lastCategory {
		err := notifier.notifyCategoryChange(category, *lastCategory, gas)
		if err != nil {
			return errors.Wrap(err, "while notifying of price category change")
		}

		log.Print("sent email to notify of price category change")
	}

	currGasPrice := prices.GasPriceData{
		Price:     gas,
		Timestamp: time.Now(),
		Category:  category,
	}
	if err := updateGasPrices(svc, gasPrices, &currGasPrice); err != nil {
		return errors.Wrap(err, "while writing gas prices")
	}

	return nil
}

type gasResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Result  struct {
		LastBlock       string `json:"LastBlock"`
		SafeGasPrice    string `json:"SafeGasPrice"`
		ProposeGasPrice string `json:"ProposeGasPrice"`
		FastGasPrice    string `json:"FastGasPrice"`
	} `json:"result"`
}

func getMediumGas(ctx context.Context, client *http.Client, apiKey string) (int, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return -1, errors.Wrap(err, "while parsing URL")
	}
	q := u.Query()
	q.Set("module", "gastracker")
	q.Set("action", "gasoracle")
	q.Set("apikey", apiKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return -1, errors.Wrap(err, "while constructing http request")
	}

	rsp, err := client.Do(req)
	if err != nil {
		return -1, errors.Wrap(err, "while making http request")
	}

	defer rsp.Body.Close()

	if rsp.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(rsp.Body)
		if err == nil {
			return -1, errors.Errorf("response error: %s %s", rsp.Status, string(body))
		}

		return -1, errors.Wrapf(err, "response error: %s", rsp.Status)
	}

	body, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return -1, errors.Wrap(err, "while reading response body")
	}

	var gas gasResponse
	if err = json.Unmarshal(body, &gas); err != nil {
		return -1, errors.Wrap(err, "while unmarshalling response body")
	}

	if gas.Status != "1" || gas.Message != "OK" {
		return -1, errors.Errorf("error response body: %s %s", gas.Status, gas.Message)
	}

	mediumGas, err := strconv.ParseInt(gas.Result.ProposeGasPrice, 10, 32)
	if err != nil {
		return -1, errors.Wrapf(err, "while parsing gas price %s", gas.Result.ProposeGasPrice)
	}

	return int(mediumGas), nil
}

func readGas(svc *dynamodb.DynamoDB) ([]prices.GasPriceData, error) {
	result, err := svc.Scan(&dynamodb.ScanInput{
		Select:    aws.String(dynamodb.SelectAllAttributes),
		TableName: aws.String(tableName),
	})
	if err != nil {
		return nil, err
	}

	log.Printf("read %d gas price records", *result.Count)

	gasPrices := make([]prices.GasPriceData, *result.Count)
	for i := range result.Items {
		var price prices.GasPriceData
		if err := dynamodbattribute.UnmarshalMap(result.Items[i], &price); err != nil {
			return nil, err
		}

		gasPrices[i] = price
	}

	return gasPrices, nil
}

func updateGasPrices(
	svc *dynamodb.DynamoDB,
	gasPrices []prices.GasPriceData,
	currGasPrice *prices.GasPriceData,
) error {
	if len(gasPrices) >= maxNumGasPrices {
		if err := deleteOldestGasPrice(svc, gasPrices); err != nil {
			return errors.Wrap(err, "while deleting oldest gas price")
		}
	}

	return writeNewGasPrice(svc, currGasPrice)
}

func deleteOldestGasPrice(svc *dynamodb.DynamoDB, gasPrices []prices.GasPriceData) error {
	var oldestGasPrice *prices.GasPriceData
	for i := range gasPrices {
		if oldestGasPrice == nil || gasPrices[i].Timestamp.Before(oldestGasPrice.Timestamp) {
			oldestGasPrice = &gasPrices[i]
		}
	}

	if oldestGasPrice == nil {
		return errors.New("could not find oldest gas price")
	}

	timestampStr := oldestGasPrice.Timestamp.Format(time.RFC3339)

	_, err := svc.DeleteItem(
		&dynamodb.DeleteItemInput{
			Key: map[string]*dynamodb.AttributeValue{
				"timestamp": {
					S: aws.String(timestampStr),
				},
			},
			TableName: aws.String(tableName),
		},
	)

	if err == nil {
		log.Print("deleted oldest gas price with timestamp ", timestampStr)
	}
	return err
}

func writeNewGasPrice(svc *dynamodb.DynamoDB, currGasPrice *prices.GasPriceData) error {
	av, err := dynamodbattribute.MarshalMap(currGasPrice)
	if err != nil {
		return err
	}

	input := &dynamodb.PutItemInput{
		Item:      av,
		TableName: aws.String(tableName),
	}

	if _, err = svc.PutItem(input); err != nil {
		return err
	}

	log.Print("wrote new gas price to DB")

	return nil
}

func getPriceStats(gasPrices []prices.GasPriceData) (*prices.PriceStats, error) {
	if len(gasPrices) == 0 {
		return nil, errors.New("no gas prices")
	}

	mean := calculateMean(gasPrices)
	stddev := calculateStdDev(gasPrices, mean)

	return &prices.PriceStats{Mean: mean, Stddev: stddev}, nil
}

func calculateMean(gasPrices []prices.GasPriceData) float64 {
	var sum float64

	for i := range gasPrices {
		sum += float64(gasPrices[i].Price)
	}

	return sum / float64(len(gasPrices))
}

func calculateStdDev(gasPrices []prices.GasPriceData, mean float64) float64 {
	if len(gasPrices) == 1 {
		return 0.0
	}

	var sumSquares float64

	for i := range gasPrices {
		diff := float64(gasPrices[i].Price) - mean
		sumSquares += diff * diff
	}

	variance := sumSquares / float64(len(gasPrices)-1)
	return math.Sqrt(variance)
}

type emailNotifier struct {
	fromAddr string
	toAddr   string
	password string
	smtpHost string
	smtpPort int
}

func newEmailNotifier() (emailNotifier, error) {
	from := os.Getenv("GAS_NOTIFIER_FROM")
	if from == "" {
		return emailNotifier{}, errors.New("GAS_NOTIFIER_FROM not set")
	}
	to := os.Getenv("GAS_NOTIFIER_TO")
	if to == "" {
		return emailNotifier{}, errors.New("GAS_NOTIFIER_TO not set")
	}
	pass := os.Getenv("GAS_NOTIFIER_PASSWORD")
	if pass == "" {
		return emailNotifier{}, errors.New("GAS_NOTIFIER_PASSWORD not set")
	}

	return emailNotifier{
		fromAddr: from,
		toAddr:   to,
		password: pass,
		smtpHost: "smtp.gmail.com",
		smtpPort: 587,
	}, nil
}

func (n *emailNotifier) notifyCategoryChange(
	newCategory, previousCategory prices.PriceCategory, currentPrice int,
) error {
	body := fmt.Sprintf(
		"Ethereum gas prices are no longer %s, they are now %s\n\nSpecifically, medium gas is now %d\n",
		previousCategory,
		newCategory,
		currentPrice,
	)

	msg := "From: " + n.fromAddr + "\n" +
		"To: " + n.toAddr + "\n" +
		fmt.Sprintf("Subject: Gas Prices are %s\n\n", newCategory) +
		body

	return smtp.SendMail(
		fmt.Sprintf("%s:%d", n.smtpHost, n.smtpPort),
		smtp.PlainAuth("", n.fromAddr, n.password, n.smtpHost),
		n.fromAddr,
		[]string{n.toAddr},
		[]byte(msg),
	)
}

func getLastCategory(gasPrices []prices.GasPriceData) *prices.PriceCategory {
	var lastPrice *prices.GasPriceData
	for i := range gasPrices {
		if lastPrice == nil || gasPrices[i].Timestamp.After(lastPrice.Timestamp) {
			lastPrice = &gasPrices[i]
		}
	}

	if lastPrice == nil {
		return nil
	}

	return &lastPrice.Category
}
