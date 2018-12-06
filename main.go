package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const KelvinShift = 273.15

func main() {
	mw := multiWeatherProvider {
		openWeatherMap{},
		darkSky{apiKey: "16f1d1a16039f72b7fb8af35ae20fe80"},
	}

	// Urbandale 41.6267° N, 93.7122° W
	// Bucharest 44.4268° N, 26.1025° E

	http.HandleFunc("/weather/", func(w http.ResponseWriter, r *http.Request) {
		begin := time.Now()
		city := strings.SplitN(r.URL.Path, "/", 3)[2]
		lat := 0.0
		long := 0.0

		lat, long, temp, err := mw.temperature(city, lat, long)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		displayLat := fmt.Sprintf("%.4f", lat)
		displayLong := fmt.Sprintf("%.4f", long)
		displayTemp := fmt.Sprintf("%.2f°C", temp)
		log.Printf("city:%s, latitude:%s, longitude:%s, temperature:%s", city, displayLat, displayLong, displayTemp)

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"city": city,
			"lat": displayLat,
			"long": displayLong,
			"temp": displayTemp,
			"took": time.Since(begin).String(),
		})
	})

	http.ListenAndServe(":8080", nil)
}

type weatherData struct {
	Celsius float64 `json:"c"`
	Fahrenheit float64 `json:"k"`
	Kelvin float64 `json:"k"`
	Latitude float64 `json:"lat"`
	Longitude float64 `json:"long"`
}

type weatherProvider interface {
	temperature(city string, lat float64, long float64) (weatherData, error) // returns temp in celsius
}

type multiWeatherProvider []weatherProvider

func (w multiWeatherProvider) temperature(city string, lat float64, long float64) (float64, float64, float64, error) {
	sum := 0.0
	n := 0

	for _, provider := range w {
		wd, err := provider.temperature(city, lat, long)
		if err != nil {
			return 0, 0, 0, err
		}

		// we will assume that 0 degrees kelvin is a bad measurement ;)
		if (wd.Kelvin > 0) {
			n += 1
			sum += wd.Celsius
		}

		if (wd.Latitude != 0.0 && wd.Longitude != 0.0 && lat == 0.0 && long == 0.0) {
			lat = wd.Latitude
			long = wd.Longitude
		}
	}

	avg := sum / float64(n)
	// log.Printf("DEBUG avg temp %.2f°C", avg)

	return lat, long, avg, nil
}

type openWeatherMap struct{}

func (w openWeatherMap) temperature(city string, lat float64, long float64) (weatherData, error) {
	// NOTE this api only uses the city string
	url := fmt.Sprintf("http://api.openweathermap.org/data/2.5/weather?APPID=aa863cffe90108e0d8be0840d87de50f&q=%s", city)
	// log.Printf("DEBUG openWeatherMap url: %s", url)
	resp, err := http.Get(url)
	if err != nil {
		return weatherData{}, err
	}

	defer resp.Body.Close()

	// define the "query"
	var d struct {
		Main struct {
			Kelvin float64 `json:"temp"`
		} `json:"main"`
		Coord struct {
			Latitude float64 `json:"lat"`
			Longitude float64 `json:"lon"`
		} `json:"coord"`
	}

	// grab the data
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return weatherData{}, err
	}

	// do the conversions
	k := d.Main.Kelvin
	c, err := kelvinToCelsius(k)
	if err != nil {
		return weatherData{}, err
	}

	f, err := kelvinToFahrenheit(k)

	// log.Printf("DEBUG conversion - original %.4f = %.4f°K to %.4f°C and %.4f°F", d.Main.Kelvin, k, c, f)

	// build the data response
	wd := weatherData {
		Celsius: c,
		Fahrenheit: f,
		Kelvin: k,
	}

	if lat == 0 && long == 0 {
		if d.Coord.Latitude != 0.0 && d.Coord.Longitude != 0.0 {
			wd.Latitude = d.Coord.Latitude
			wd.Longitude = d.Coord.Longitude
			log.Printf("Latitude %.4f and longitude %.4f returned for %s", d.Coord.Latitude, d.Coord.Longitude, city)
		} else {
			log.Printf("No latitude and longitude returned for %s", city)
		}
	}

	// log.Printf("DEBUG openWeatherMap: %s: %.2f°C (%.4f, %.4f)", city, wd.Celsius, wd.Latitude, wd.Longitude)
	return wd, nil
}

type darkSky struct {
	apiKey string
}

func (w darkSky) temperature(city string, lat float64, long float64) (weatherData, error) {
	// NOTE this api only uses the latitude and longitude
	url := fmt.Sprintf("https://api.darksky.net/forecast/%s/%.4f,%.4f?exclude=minutely,hourly,daily,alerts", w.apiKey, lat, long)
	// log.Printf(url)
	resp, err := http.Get(url)
	if err != nil {
		return weatherData{}, err
	}

	// can't use this api without latitude and longitude
	if lat == 0.0 && long == 0.0 {
		log.Printf("No lat and long canceling darkSky call")
		return weatherData{}, err
	}

	defer resp.Body.Close()

	// define the "query"
	var d struct {
		Currently struct {
			Temperature float64 `json:"temperature"`
		} `json:"currently"`
		Flags struct {
			Units string `json:"units"`
		} `json:"flags"`
	}

	// grab the data
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return weatherData{}, err
	}

	// do the conversions
	c := 0.0
	f := 0.0
	u := d.Flags.Units

	// working around a bug in dark sky's algorithm seems to always return us units
	// but i want to plan for it maybe being fixed at some point
	if strings.Compare(u, "us") == 0 {
		f = d.Currently.Temperature
		c, err = fahrenheitToCelsius(f)
		if err != nil {
			log.Printf("failed to convert %.4f°F to Celsius. %s", f, err)
			return weatherData{}, err
		}
	} else if strings.Compare(u, "si") == 0 {
		c = d.Currently.Temperature
		f, err = celsiusToFahrenheit(c)
		if err != nil {
			log.Printf("failed to convert %.4f°C to Fahrenheit. %s", c, err)
			return weatherData{}, err
		}
	} else {
		log.Printf("unexpected unit type %s", u)
		return weatherData{}, err
	}
	k, err := celsiusToKelvin(c)
	if err != nil {
		log.Printf("failed to convert %.4f°C to Kelvin. %s", c, err)
		return weatherData{}, err
	}

	wd := weatherData {
		Celsius: c,
		Fahrenheit: f,
		Kelvin: k,
		Latitude: lat,
		Longitude: long,
	}

	// log.Printf("DEBUG dark sky: %s: %.2f°C (%.4f, %.4f)", city, wd.Celsius, wd.Latitude, wd.Longitude)
	return wd, nil
}

func celsiusToKelvin(c float64) (float64, error) {
	if c < -KelvinShift {
		return 0, errors.New("celsiusToKelvin: Out of Range")
	}
	return c + KelvinShift, nil
}

func kelvinToCelsius(k float64) (float64, error) {
	if k < 0 {
		return 0, errors.New("kelvinToCelsius: Out of Range")
	}
	return k - KelvinShift, nil
}

func fahrenheitToCelsius(f float64) (float64, error) {
	if f < -459.67 {
		return 0, errors.New("fahrenheitToCelsius: Out of Range")
	}
	return (f - 32) * 5 / 9, nil
}

func celsiusToFahrenheit(c float64) (float64, error) {
	if c < -KelvinShift {
		return 0, errors.New("celsiusToFahrenheit: Out of Range")
	}
	return (c * 9 / 5) + 32, nil
}

func kelvinToFahrenheit(k float64) (float64, error) {
	if k < 0 {
		return 0, errors.New("kelvinToFahrenheit: Out of Range")
	}
	return ((k - KelvinShift) * 9 / 5) + 32, nil
}
