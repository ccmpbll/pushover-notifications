package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

func fakePlugs(status int, body string, err error) func(context.Context, routesapi.CallParams) (routesapi.Response, error) {
	return func(_ context.Context, p routesapi.CallParams) (routesapi.Response, error) {
		if p.Module != tasmotaModule || p.Method != http.MethodGet || p.Path != "/plugs" {
			return routesapi.Response{}, errors.New("an unexpected call: " + p.Module + " " + p.Method + " " + p.Path)
		}

		return routesapi.Response{Status: status, Body: []byte(body)}, err
	}
}

const plugList = `{"items":[
	{"name":"a","printer":"SER-A","on":true,"energy":{"total_kwh":10.5}},
	{"name":"b","printer":"SER-A","on":true,"energy":{"total_kwh":2.25}},
	{"name":"c","printer":"SER-B","on":true,"energy":{"total_kwh":99}},
	{"name":"d","on":true,"energy":{"total_kwh":5}}
],"price_per_kwh":0.14,"currency":"$"}`

func TestThePowerOfAPrintersPlugsIsAddedUp(t *testing.T) {
	p := plugPower{call: fakePlugs(200, plugList, nil)}

	got, ok := p.Read(context.Background(), "SER-A")
	if !ok || got.kWh != 12.75 || got.plugs != 2 {
		t.Fatalf("%+v ok=%v", got, ok)
	}

	if got.price == nil || *got.price != 0.14 || got.currency != "$" {
		t.Errorf("price %v currency %q", got.price, got.currency)
	}
}

func TestAPrinterWithNoMeteredPlugHasNoReading(t *testing.T) {
	p := plugPower{call: fakePlugs(200, plugList, nil)}

	for _, serial := range []string{"SER-NOPE", ""} {
		if _, ok := p.Read(context.Background(), serial); ok {
			t.Errorf("%q has a reading", serial)
		}
	}

	noMeter := `{"items":[{"name":"a","printer":"SER-A","on":true}]}`
	if _, ok := (plugPower{call: fakePlugs(200, noMeter, nil)}).Read(context.Background(), "SER-A"); ok {
		t.Error("a plug with no meter has a reading")
	}

	// One of two plugs reports no total: the sum would be wrong, so none is given.
	half := `{"items":[{"name":"a","printer":"SER-A","energy":{"total_kwh":1}},{"name":"b","printer":"SER-A","energy":{"watts":3}}]}`
	if _, ok := (plugPower{call: fakePlugs(200, half, nil)}).Read(context.Background(), "SER-A"); ok {
		t.Error("a half-metered printer has a reading")
	}
}

func TestAFailedOrOddCallIsNoReading(t *testing.T) {
	for name, call := range map[string]func(context.Context, routesapi.CallParams) (routesapi.Response, error){
		"not installed": fakePlugs(0, "", routesapi.ErrCallUnavailable),
		"refused":       fakePlugs(403, `{}`, nil),
		"not json":      fakePlugs(200, `<html>`, nil),
		"no call":       nil,
	} {
		if _, ok := (plugPower{call: call}).Read(context.Background(), "SER-A"); ok {
			t.Errorf("%s: a reading", name)
		}
	}
}

func TestPowerUsedIsTheDifferenceAndItsCost(t *testing.T) {
	price := 0.14
	start := powerReading{kWh: 10, plugs: 1, price: &price, currency: "$"}

	got := energyBetween(start, powerReading{kWh: 10.42, plugs: 1, price: &price, currency: "$"})
	if got["energy"] != "0.42 kWh" || got["cost"] != "$0.06" {
		t.Errorf("%v", got)
	}

	// No price set: the energy, and no cost.
	got = energyBetween(powerReading{kWh: 1, plugs: 1}, powerReading{kWh: 2.5, plugs: 1})
	if got["energy"] != "1.50 kWh" || got["cost"] != "" {
		t.Errorf("%v", got)
	}

	// A currency written in letters follows the number.
	eur := 0.30
	got = energyBetween(powerReading{kWh: 0, plugs: 1, price: &eur, currency: "EUR"}, powerReading{kWh: 2, plugs: 1, price: &eur, currency: "EUR"})
	if got["cost"] != "0.60 EUR" {
		t.Errorf("%v", got)
	}
}

func TestPowerUsedIsLeftOutWhenTheNumbersCannotBeTrusted(t *testing.T) {
	one := powerReading{kWh: 5, plugs: 1}

	for name, end := range map[string]powerReading{
		"the total went down (a plug was reset)": {kWh: 4, plugs: 1},
		"a plug appeared":                        {kWh: 6, plugs: 2},
	} {
		if got := energyBetween(one, end); len(got) != 0 {
			t.Errorf("%s: %v", name, got)
		}
	}
}
