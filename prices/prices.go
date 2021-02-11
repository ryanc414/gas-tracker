package prices

import "time"

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
