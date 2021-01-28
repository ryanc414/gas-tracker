package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/pkg/errors"
)

const (
	baseURL         = "https://api.etherscan.io/api"
	pricesFilename  = "gas_prices.json"
	maxNumGasPrices = 7 * 24 // 7 days of data, assuming run once per hour.
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	apiKey := os.Getenv("ETHERSCAN_API_KEY")
	if apiKey == "" {
		return errors.New("ETHERSCAN_API_KEY is not set")
	}
	gas, err := getMediumGas(apiKey)
	if err != nil {
		return err
	}
	fmt.Println("medium gas is", gas)

	gasPrices, err := appendToFile(gas)
	if err != nil {
		return err
	}
	fmt.Println("written gas price to file")

	stats, err := getPriceStats(gasPrices)
	if err != nil {
		return err
	}
	fmt.Printf("mean price = %v, stddev = %v\n", stats.mean, stats.stddev)

	category := categorisePrice(gas, stats)
	fmt.Println("the price now is", category)

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

type gasPriceData struct {
	Price     int       `json:"price"`
	Timestamp time.Time `json:"timestamp"`
}

func appendToFile(newGasPrice int) ([]gasPriceData, error) {
	gasPrices, err := readGasPrices()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			gasPrices = nil
		} else {
			return nil, errors.Wrap(err, "while reading gas prices")
		}
	}

	gasPrices = append(gasPrices, gasPriceData{Price: newGasPrice, Timestamp: time.Now()})
	if len(gasPrices) > maxNumGasPrices {
		gasPrices = gasPrices[len(gasPrices)-maxNumGasPrices:]
	}

	return gasPrices, writeGasPrices(gasPrices)
}

func readGasPrices() ([]gasPriceData, error) {
	data, err := ioutil.ReadFile(pricesFilename)
	if err != nil {
		return nil, err
	}

	var gasPrices []gasPriceData
	if err := json.Unmarshal(data, &gasPrices); err != nil {
		return nil, err
	}

	return gasPrices, nil
}

func writeGasPrices(gasPrices []gasPriceData) error {
	data, err := json.Marshal(gasPrices)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(pricesFilename, data, 0644)
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
