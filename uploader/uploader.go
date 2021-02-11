package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/ryanc414/gas-tracker/prices"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

type convertedPrice struct {
	Price     int    `dynamodbav:"price"`
	Timestamp string `dynamodbav:"timestamp"`
	Category  string `dynamodbav:"category"`
}

func run(context.Context) error {
	priceData, err := getItems()
	if err != nil {
		return err
	}

	converted := convertPrices(priceData)
	return uploadPrices(converted)
}

const tableName = "gasPrices"

func uploadPrices(converted []convertedPrice) error {
	// Initialize a session that the SDK will use to load
	// credentials from the shared credentials file ~/.aws/credentials
	// and region from the shared configuration file ~/.aws/config.
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))

	// Create DynamoDB client
	svc := dynamodb.New(sess)

	for i := range converted {
		av, err := dynamodbattribute.MarshalMap(converted[i])
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

		ts := converted[i].Timestamp

		fmt.Println("Successfully added " + ts + " to table " + tableName)
	}

	fmt.Println("successfully added all items to table")

	return nil
}

func getItems() (*prices.HistoricalGasPrices, error) {
	raw, err := ioutil.ReadFile("/users/ryan/.gas_prices.json")
	if err != nil {
		return nil, err
	}

	var prices prices.HistoricalGasPrices
	if err := json.Unmarshal(raw, &prices); err != nil {
		return nil, err
	}

	return &prices, nil
}

func convertPrices(priceData *prices.HistoricalGasPrices) []convertedPrice {
	converted := make([]convertedPrice, len(priceData.Prices))

	for i := range priceData.Prices {
		converted[i] = convertedPrice{
			Price:     priceData.Prices[i].Price,
			Timestamp: priceData.Prices[i].Timestamp.Format(time.RFC3339),
			Category:  prices.Average.String(),
		}
	}

	converted[len(priceData.Prices)-1].Category = priceData.LastCategory.String()

	return converted
}
