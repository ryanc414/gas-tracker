package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/pkg/errors"
)

const (
	baseURL         = "https://api.etherscan.io/api"
	pricesFilename  = ".gas_prices.json"
	maxNumGasPrices = 7 * 24 // 7 days of data, assuming run once per hour.
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	apiKey := os.Getenv("ETHERSCAN_API_KEY")
	if apiKey == "" {
		return errors.New("ETHERSCAN_API_KEY is not set")
	}

	notifier, err := newEmailNotifier()
	if err != nil {
		return errors.Wrap(err, "while constructing email notifier")
	}

	gas, err := getMediumGas(apiKey)
	if err != nil {
		return errors.Wrap(err, "while getting current gas price")
	}
	log.Print("medium gas is ", gas)

	filepath, err := getFilepath()
	if err != nil {
		return errors.Wrap(err, "while getting filepath")
	}

	gasPrices, err := readAndAppend(gas, filepath)
	if err != nil {
		return errors.Wrap(err, "while reading gas prices from file")
	}
	log.Print("written gas price to file")

	stats, err := getPriceStats(gasPrices.Prices)
	if err != nil {
		return errors.Wrap(err, "while calcuating gas price stats")
	}
	log.Printf("mean price = %v, stddev = %v", stats.mean, stats.stddev)

	category := categorisePrice(gas, stats)
	log.Print("the price now is ", category)

	if category != Average && category != gasPrices.LastCategory {
		err := notifier.notifyCategoryChange(category, gasPrices.LastCategory, gas)
		if err != nil {
			return errors.Wrap(err, "while notifying of price category change")
		}

		log.Print("sent email to notify of price category change")
	}

	gasPrices.LastCategory = category
	if err := writeGasPrices(filepath, &gasPrices); err != nil {
		return errors.Wrap(err, "while writing gas prices to file")
	}

	return nil
}

func getFilepath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "while getting user home dir")
	}
	path := filepath.Join(home, pricesFilename)

	return path, nil
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

func getMediumGas(apiKey string) (int, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return -1, errors.Wrap(err, "while parsing URL")
	}
	q := u.Query()
	q.Set("module", "gastracker")
	q.Set("action", "gasoracle")
	q.Set("apikey", apiKey)
	u.RawQuery = q.Encode()

	rsp, err := http.Get(u.String())
	if err != nil {
		return -1, errors.Wrap(err, "while requesting etherscan API")
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

type historicalGasPrices struct {
	LastCategory priceCategory  `json:"price_category"`
	Prices       []gasPriceData `json:"prices"`
}

type gasPriceData struct {
	Price     int       `json:"price"`
	Timestamp time.Time `json:"timestamp"`
}

func readAndAppend(newGasPrice int, filename string) (historicalGasPrices, error) {
	gasPrices, err := readGasPrices(filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			gasPrices = historicalGasPrices{LastCategory: Average, Prices: nil}
		} else {
			return historicalGasPrices{}, errors.Wrap(err, "while reading gas prices")
		}
	}

	gasPrices.Prices = append(gasPrices.Prices, gasPriceData{Price: newGasPrice, Timestamp: time.Now()})
	if len(gasPrices.Prices) > maxNumGasPrices {
		gasPrices.Prices = gasPrices.Prices[len(gasPrices.Prices)-maxNumGasPrices:]
	}

	return gasPrices, nil
}

func readGasPrices(filename string) (historicalGasPrices, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return historicalGasPrices{}, err
	}

	var history historicalGasPrices
	if err := json.Unmarshal(data, &history); err != nil {
		return historicalGasPrices{}, err
	}

	return history, nil
}

func writeGasPrices(filename string, gasPrices *historicalGasPrices) error {
	data, err := json.Marshal(gasPrices)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(filename, data, 0644)
}

type priceStats struct {
	mean   float64
	stddev float64
}

func getPriceStats(gasPrices []gasPriceData) (*priceStats, error) {
	if len(gasPrices) == 0 {
		return nil, errors.New("no gas prices")
	}

	mean := calculateMean(gasPrices)
	stddev := calculateStdDev(gasPrices, mean)

	return &priceStats{mean: mean, stddev: stddev}, nil
}

func calculateMean(gasPrices []gasPriceData) float64 {
	var sum float64

	for i := range gasPrices {
		sum += float64(gasPrices[i].Price)
	}

	return sum / float64(len(gasPrices))
}

func calculateStdDev(gasPrices []gasPriceData, mean float64) float64 {
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

type priceCategory int

const (
	High priceCategory = iota
	Average
	Low
)

func (p priceCategory) String() string {
	switch p {
	case High:
		return "High"

	case Average:
		return "Average"

	case Low:
		return "Low"

	default:
		panic("unexpected price category")
	}
}

func categorisePrice(price int, stats *priceStats) priceCategory {
	fprice := float64(price)

	if fprice < (stats.mean - stats.stddev) {
		return Low
	}

	if fprice > (stats.mean + stats.stddev) {
		return High
	}

	return Average
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
	newCategory, previousCategory priceCategory, currentPrice int,
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
