package main

import (
	"encoding/json"
	"fmt"
	"flag"
	"log"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/encratite/commons"
	"github.com/fatih/color"
)

const (
	percent = 100.0
)

type Configuration struct {
	Strategies []Strategy `yaml:"strategies"`
}

type Strategy struct {
	Name string `yaml:"name"`
	Currency string `yaml:"currency"`
	Offset int `yaml:"offset"`
	GreaterThan *float64 `yaml:"greaterThan"`
	LessThan *float64 `yaml:"lessThan"`
	Weekdays []commons.SerializableWeekday `yaml:"weekdays"`
	Times []commons.SerializableDuration `yaml:"times"`
	Up bool `yaml:"up"`
}

type ohlcRecord struct {
	timestamp time.Time
	open float64
	high float64
	low float64
	close float64
}

var configuration *Configuration

func main() {
	strategyName := flag.String("strategy", "", "Restrict evaluation of strategies to the one matching this string")
	flag.Parse()
	loadConfiguration()
	evaluateStrategies(*strategyName)
}

func loadConfiguration() {
	configuration = commons.LoadConfiguration[Configuration]("configuration/configuration.yaml")
	configuration.validate()
}

func evaluateStrategies(strategyName string) {
	fmt.Printf("\n")
	for _, strategy := range configuration.Strategies {
		if strategyName != "" && strategy.Name != strategyName {
			continue
		}
		strategy.evaluate()
	}
}

func (c *Configuration) validate() {
	for _, strategy := range c.Strategies {
		if strategy.Name == "" {
			log.Fatalf("Missing strategy name")
		}
		if strategy.Currency == "" {
			log.Fatalf("Missing currency name for strategy %s", strategy.Name)
		}
		if strategy.Offset <= 0 {
			log.Fatalf("Invalid offset for strategy %s", strategy.Name)
		}
		if strategy.GreaterThan == nil && strategy.LessThan == nil {
			log.Fatalf("Missing momentum constraint for strategy %s", strategy.Name)
		}
	}
}

func (s *Strategy) evaluate() {
	records := loadRecords(s.Currency)
	now := time.Now().UTC()
	weekday := now.Weekday()
	weekdays := []time.Weekday{}
	weekdayNames := []string{}
	for _, w := range s.Weekdays {
		weekdays = append(weekdays, w.Weekday)
		weekdayNames = append(weekdayNames, fmt.Sprintf("%s", w.Weekday))
	}
	timeStrings := []string{}
	for _, t := range s.Times {
		timeString := commons.GetTimeOfDayString(t.Duration)
		timeStrings = append(timeStrings, timeString)
	}
	weekdayMatch := slices.Contains(weekdays, weekday)
	if !weekdayMatch {
		return
	}
	timeMatch := false
	for _, t := range s.Times {
		nextHour := now.Hour() + 1
		hours := int(t.Hours())
		if nextHour == hours {
			timeMatch = true
			break
		}
	}
	momentumTime := now.Add(time.Duration(1 - s.Offset) * time.Hour)
	truncatedTime := time.Date(
		momentumTime.Year(),
		momentumTime.Month(),
		momentumTime.Day(),
		momentumTime.Hour(),
		0,
		0,
		0,
		momentumTime.Location(),
	)
	momentumMatch := false
	momentum := math.NaN()
	lastIndex := len(records) - 1
	latestRecord := records[lastIndex]
	var momentumRecord ohlcRecord
	foundRecord := false
	for i := range records {
		record := records[lastIndex - i]
		if !record.timestamp.After(truncatedTime) {
			momentum = (latestRecord.close / record.open - 1.0) * percent
			match := true
			if s.GreaterThan != nil {
				match = match && momentum > *s.GreaterThan
			}
			if s.LessThan != nil {
				match = match && momentum < *s.LessThan
			}
			momentumMatch = match
			momentumRecord = record
			foundRecord = true
			break
		}
	}
	blue := color.New(color.FgBlue).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	fmt.Printf("%s:\n", s.Name)
	fmt.Printf("\tCurrency: %s\n", blue(s.Currency))
	fmt.Printf("\tWeekdays: %s\n", strings.Join(weekdayNames, ", "))
	fmt.Printf("\tTimes: %s\n", strings.Join(timeStrings, ", "))
	fmt.Printf("\tMomentum offset: %dh\n", s.Offset)
	if s.GreaterThan != nil {
		fmt.Printf("\tGreater than: %.2f%%\n", *s.GreaterThan)
	}
	if s.LessThan != nil {
		fmt.Printf("\tLess than: %.2f%%\n", *s.LessThan)
	}
	var sideString string
	if s.Up {
		sideString = green("Up")
	} else {
		sideString = red("Down")
	}
	fmt.Printf("\tSide: %s\n", sideString)
	fmt.Printf("\tCurrent price: %.4f\n", latestRecord.close)
	if foundRecord {
		fmt.Printf("\tMomentum price: %.4f\n", momentumRecord.close)
		fmt.Printf("\tMomentum time: %s UTC\n", commons.GetTimeString(momentumRecord.timestamp))
	} else {
		fmt.Printf("\tMomentum price: %s\n", red("missing"))
	}
	fmt.Printf("\tCurrent weekday: %s (%s)\n", weekday, formatBool(weekdayMatch))
	fmt.Printf("\tCurrent time of day: %02d:%02d UTC (%s)\n", now.Hour(), now.Minute(), formatBool(timeMatch))
	fmt.Printf("\tCurrent momentum: %+.2f%% (%s)\n", momentum, formatBool(momentumMatch))
	if weekdayMatch && timeMatch && momentumMatch {
		fmt.Printf("\n\tAll conditions match, open \"%s\" position\n", sideString)
	}
	fmt.Printf("\n")
}

func loadRecords(currency string) []ohlcRecord {
	now := time.Now().UTC()
	unixMilliseconds := now.UnixMilli()
	url := "https://www.binance.com/api/v3/uiKlines"
	parameters := map[string]string{
		"symbol": currency,
		"interval": "5m",
		"limit": "1000",
		"endTime": commons.Int64ToString(unixMilliseconds),
	}
	data, err := commons.DownloadJSON[[]json.RawMessage](url, parameters)
	if err != nil {
		log.Fatalf("Failed to download data from Binance: %v", err)
	}
	records := []ohlcRecord{}
	for _, recordData := range data {
		fields := []json.RawMessage{}
		err := json.Unmarshal(recordData, &fields)
		if err != nil {
			log.Fatalf("Failed to unmarshal fields")
		}
		var recordUnixMilliseconds int64
		err = json.Unmarshal(fields[0], &recordUnixMilliseconds)
		if err != nil {
			log.Fatalf("Failed to unmarshal UNIX timestamp")
		}
		timestamp := time.UnixMilli(recordUnixMilliseconds).UTC()
		unmarshalFloat := func (index int) float64 {
			var floatString string
			err = json.Unmarshal(fields[index], &floatString)
			if err != nil {
				log.Fatalf("Failed to unmarshal UNIX timestamp")
			}
			value := commons.MustParseFloat(floatString)
			return value
		}
		open := unmarshalFloat(1)
		high := unmarshalFloat(2)
		low := unmarshalFloat(3)
		close := unmarshalFloat(4)
		record := ohlcRecord{
			timestamp: timestamp,
			open: open,
			high: high,
			low: low,
			close: close,
		}
		records = append(records, record)
	}
	return records
}

func formatBool(value bool) string {
	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	output := fmt.Sprintf("%t", value)
	if value {
		output = green(output)
	} else {
		output = red(output)
	}
	return output
}