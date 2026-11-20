package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"unicode"

	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

// tasmotaModule is the namespace the Tasmota Plugs extension is mounted under.
// This module asks it for the power each printer's plugs have used, and works
// without it: the power tags are then empty and their lines are left out.
const tasmotaModule = "com.github.aux.ccmpbll.tasmota-plugs"

// powerReading is what a printer's plugs had used in all at one moment.
type powerReading struct {
	kWh   float64
	plugs int
	// price and currency are the other module's, so a cost matches what it shows.
	price    *float64
	currency string
}

// plugPower reads the plugs of a printer through the other module's routes.
type plugPower struct {
	call func(context.Context, routesapi.CallParams) (routesapi.Response, error)
}

// Read adds up the lifetime total of every plug linked to the printer. ok is
// false when the other module is not there, a plug has no meter, or a printer
// has no plug: a sum over some of the plugs would be wrong.
func (p plugPower) Read(ctx context.Context, serial string) (powerReading, bool) {
	if p.call == nil || serial == "" {
		return powerReading{}, false
	}

	answer, err := p.call(ctx, routesapi.CallParams{Module: tasmotaModule, Method: http.MethodGet, Path: "/plugs"})
	if err != nil || answer.Status != http.StatusOK {
		return powerReading{}, false
	}

	var list struct {
		Items []struct {
			Printer string `json:"printer"`
			Energy  *struct {
				TotalKWh *float64 `json:"total_kwh"`
			} `json:"energy"`
		} `json:"items"`
		Price    *float64 `json:"price_per_kwh"`
		Currency string   `json:"currency"`
	}

	if json.Unmarshal(answer.Body, &list) != nil {
		return powerReading{}, false
	}

	reading := powerReading{price: list.Price, currency: list.Currency}

	for _, item := range list.Items {
		if item.Printer != serial {
			continue
		}

		if item.Energy == nil || item.Energy.TotalKWh == nil {
			return powerReading{}, false
		}

		reading.kWh += *item.Energy.TotalKWh
		reading.plugs++
	}

	return reading, reading.plugs > 0
}

// energyBetween is the {energy} and {cost} tags for a print, from the readings
// at its start and its end. It is empty when the two cannot be compared: the
// plugs changed, or a total went down because a plug was reset.
func energyBetween(start, end powerReading) map[string]string {
	if start.plugs != end.plugs || end.kWh < start.kWh {
		return nil
	}

	used := end.kWh - start.kWh
	out := map[string]string{"energy": fmt.Sprintf("%.2f kWh", used)}

	if end.price != nil {
		out["cost"] = money(used**end.price, end.currency)
	}

	return out
}

// money writes an amount with its currency: a symbol goes before the number,
// and a code such as EUR after it.
func money(amount float64, currency string) string {
	if currency == "" {
		return fmt.Sprintf("%.2f", amount)
	}

	for _, r := range currency {
		if unicode.IsLetter(r) {
			return fmt.Sprintf("%.2f %s", amount, currency)
		}
	}

	return fmt.Sprintf("%s%.2f", currency, amount)
}
