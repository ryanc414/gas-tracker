package prices

import (
	"errors"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
)

type GasPriceData struct {
	Price     int           `json:"price"`
	Timestamp time.Time     `json:"timestamp"`
	Category  PriceCategory `json:"category"`
}

type PriceCategory int

const (
	High PriceCategory = iota
	Average
	Low
)

func (p PriceCategory) String() string {
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
func (p PriceCategory) MarshalDynamoDBAttributeValue(av *dynamodb.AttributeValue) error {
	av.S = aws.String(p.String())
	return nil
}

func (p *PriceCategory) UnmarshalDynamoDBAttributeValue(av *dynamodb.AttributeValue) error {
	if av.S == nil {
		return nil
	}

	val, err := parsePriceCategory(av.S)
	if err != nil {
		return err
	}

	*p = val
	return nil
}

func parsePriceCategory(input *string) (PriceCategory, error) {
	switch *input {
	case "High":
		return High, nil

	case "Average":
		return Average, nil

	case "Low":
		return Low, nil

	default:
		return Average, errors.New("unexpected price category")
	}
}

func CategorisePrice(price int, stats *PriceStats) PriceCategory {
	fprice := float64(price)

	if fprice < (stats.Mean - stats.Stddev) {
		return Low
	}

	if fprice > (stats.Mean + stats.Stddev) {
		return High
	}

	return Average
}

type PriceStats struct {
	Mean   float64
	Stddev float64
}
