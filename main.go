package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/deckarep/gosx-notifier"
	"github.com/shopspring/decimal"
	bittrex "github.com/toorop/go-bittrex"
	chart "github.com/wcharczuk/go-chart"
	"github.com/wcharczuk/go-chart/drawing"
)

const (
	volumeThreshold  = 200.0
	pumpThreshold    = 1.75
	dumpThreshold    = 0.75
	bittrexMarketUrl = "https://bittrex.com/Market/Index?MarketName="
)

var client = bittrex.New("", "")
var logoDir = filepath.Join(os.TempDir(), "bittrex_logos")

func notify(market bittrex.MarketSummary, wg *sync.WaitGroup) {
	lowChange, _ := market.Last.Sub(market.Low).Div(market.Low).Mul(decimal.NewFromFloat(100.0)).Float64()
	highChange, _ := market.Last.Sub(market.High).Div(market.High).Mul(decimal.NewFromFloat(100.0)).Float64()
	volume, _ := market.BaseVolume.Float64()
	note := gosxnotifier.NewNotification(fmt.Sprintf("L %0.1f%% | H %0.1f%% | %0.0fb",
		lowChange, highChange, volume))
	note.Title = market.MarketName
	note.Subtitle = "Bittrex"
	note.Group = "com." + market.MarketName + ".price"
	note.Sound = gosxnotifier.Basso
	note.Link = fmt.Sprintf("%s%s", bittrexMarketUrl, market.MarketName)
	note.AppIcon = fmt.Sprintf("%s/%s.png", logoDir, market.MarketName)
	chart := downloadChart(market.MarketName)
	if chart != "" {
		defer os.Remove(chart)
	}
	note.ContentImage = chart

	err := note.Push()
	if err != nil {
		log.Fatal(err)
	}
	wg.Done()
}

func filterVolume(markets []bittrex.MarketSummary) []bittrex.MarketSummary {
	var filtered []bittrex.MarketSummary
	for _, market := range markets {
		if market.BaseVolume.GreaterThan(decimal.NewFromFloat(volumeThreshold)) {
			filtered = append(filtered, market)
		}
	}
	return filtered
}

func filterPump(markets []bittrex.MarketSummary) []bittrex.MarketSummary {
	var filtered []bittrex.MarketSummary
	for _, market := range markets {
		if market.Last.Div(market.Low).GreaterThan(decimal.NewFromFloat(pumpThreshold)) {
			filtered = append(filtered, market)
		}
	}
	return filtered
}

func filterDump(markets []bittrex.MarketSummary) []bittrex.MarketSummary {
	var filtered []bittrex.MarketSummary
	for _, market := range markets {
		if market.Last.Div(market.High).LessThan(decimal.NewFromFloat(dumpThreshold)) {
			filtered = append(filtered, market)
		}
	}
	return filtered
}

func filterAltcoins(markets []bittrex.MarketSummary) []bittrex.MarketSummary {
	var filtered []bittrex.MarketSummary
	for _, market := range markets {
		if strings.HasPrefix(market.MarketName, "BTC-") {
			filtered = append(filtered, market)
		}
	}
	return filtered
}

func downloadLogo(market bittrex.Market) {
	response, err := http.Get(market.LogoUrl)
	if err != nil {
		return
	}
	defer response.Body.Close()
	file, err := os.Create(fmt.Sprintf("%s/%s-%s.png",
		logoDir,
		market.BaseCurrency,
		market.MarketCurrency))
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	_, err = io.Copy(file, response.Body)
	if err != nil {
		fmt.Println(err)
	}
}

func loadLogos() {
	if _, err := os.Stat(logoDir); err == nil {
		return
	}
	os.MkdirAll(logoDir, os.ModePerm)
	markets, err := client.GetMarkets()
	if err != nil {
		os.RemoveAll(logoDir)
		return
	}
	log.Println("Downloading logos...")
	for _, market := range markets {
		downloadLogo(market)
	}
}

func downloadChart(market string) string {
	ticks, err := client.GetTicks(market, "thirtyMin")
	if err != nil {
		log.Fatal(err)
		return ""
	}
	var y []float64
	var x []float64
	for i, tick := range ticks[len(ticks)-48:] {
		f, _ := tick.Close.Float64()
		x = append(x, float64(i+1))
		y = append(y, f)
	}
	graph := chart.Chart{
		Series: []chart.Series{
			chart.ContinuousSeries{
				XValues: x,
				YValues: y,
				Style: chart.Style{
					Show:        true,
					StrokeWidth: 25.0,
					StrokeColor: drawing.ColorBlack,
				},
			},
		},
	}
	file, err := ioutil.TempFile("", market)
	if err != nil {
		log.Fatal(err)
		return ""
	}
	defer file.Close()
	err = graph.Render(chart.PNG, file)
	if err != nil {
		os.Remove(file.Name())
		log.Fatal(err)
		return ""
	}
	return file.Name()
}

func main() {
	loadLogos()
	marketsSum, err := client.GetMarketSummaries()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	altcoins := filterVolume(filterAltcoins(marketsSum))
	dumps := filterDump(altcoins)
	pumps := filterPump(altcoins)
	var wg sync.WaitGroup
	wg.Add(len(pumps) + len(dumps))
	for _, market := range append(pumps, dumps...) {
		go notify(market, &wg)
	}
	wg.Wait()
}
