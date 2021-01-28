package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/pkg/errors"
)

const (
	baseURL        = "https://api.etherscan.io/api"
	pricesFilename = "gas_prices.json"
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

	if err := appendToFile(gas); err != nil {
		return err
	}
	fmt.Println("written gas price to file")

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

func appendToFile(newGasPrice int) error {
	gasPrices, err := readGasPrices()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			gasPrices = nil
		} else {
			return errors.Wrap(err, "while reading gas prices")
		}
	}

	gasPrices = append(gasPrices, gasPriceData{Price: newGasPrice, Timestamp: time.Now()})
	return writeGasPrices(gasPrices)
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
