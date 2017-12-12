package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/deckarep/gosx-notifier"
	"github.com/sazor/daemon"
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
	name             = "bittrex_notifier"
	description      = "OS X Notification of pump & dumps on Bittrex"
)

type Service struct {
	daemon.Daemon
}

func (service *Service) Manage() (string, error) {

	usage := "Usage: bittrex_notifier install | remove | start | stop | status"
	// If received any kind of command, do it
	if len(os.Args) > 1 {
		command := os.Args[1]
		switch command {
		case "install":
			return service.Install()
		case "remove":
			return service.Remove()
		case "start":
			return service.Start()
		case "stop":
			return service.Stop()
		case "status":
			return service.Status()
		default:
			return usage, nil
		}
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, os.Kill, syscall.SIGTERM)
	tickChan := time.NewTicker(time.Minute * 1).C
	showPumpDumps()
	for {
		select {
		case <-tickChan:
			showPumpDumps()
		case <-interrupt:
			return "Service exited", nil
			break
		default:
		}
	}
	return "Service exited", nil
}

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
		log.Println("smth")
		log.Println(err)
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

func downloadLogo(market bittrex.Market, wg sync.WaitGroup) {
	defer wg.Done()
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
	var wg sync.WaitGroup
	wg.Add(len(markets))
	for _, market := range markets {
		go downloadLogo(market, wg)
	}
	wg.Wait()
}

func downloadChart(market string) string {
	ticks, err := client.GetTicks(market, "thirtyMin")
	if err != nil {
		log.Println(err)
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
		log.Println(err)
		return ""
	}
	defer file.Close()
	err = graph.Render(chart.PNG, file)
	if err != nil {
		os.Remove(file.Name())
		log.Println(err)
		return ""
	}
	return file.Name()
}

func showPumpDumps() {
	marketsSum, err := client.GetMarketSummaries()
	if err != nil {
		log.Println(err)
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
func main() {
	loadLogos()
	srv, err := daemon.New(name, description)
	if err != nil {
		log.Println("Error: ", err)
		os.Exit(1)
	}
	service := &Service{srv}
	status, err := service.Manage()
	if err != nil {
		log.Println(status, "\nError: ", err)
		os.Exit(1)
	}
	fmt.Println(status)
}
