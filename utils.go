package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"insighta_backend/internal/database"
	"log"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
)

var AGIFY_API_URL string = "https://api.agify.io/"
var GENDERIZE_API_URL string = "https://api.genderize.io/"
var NATIONALIZE_API_URL string = "https://api.nationalize.io/"

func respondWithError(w http.ResponseWriter, statusCode int, errMsg string) {
	errObj := ErrorObject{Status: "error", Message: errMsg}
	errJSON, _ := json.Marshal(errObj)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write(errJSON)
}

func respondWithJSON(w http.ResponseWriter, resTemplate interface{}, HTTPstatus int) {
	resJSON, err := json.Marshal(resTemplate)
	if err != nil {
		log.Fatal("unable to parse response JSON")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(HTTPstatus)
	w.Write([]byte(resJSON))
}

func ageGroupFromAgify(age int) string {
	//Age group from Agify: 0–12 → child, 13–19 → teenager, 20–59 → adult, 60+ → senior
	if (age >= 0) && (age <= 12) {
		return "child"
	}
	if (age >= 13) && (age <= 19) {
		return "teenager"
	}
	if (age >= 20) && (age <= 59) {
		return "adult"
	}
	if age >= 60 {
		return "senior"
	}
	return ""

}

func getTopCountry(countries []CountryData) CountryData {
	if len(countries) == 0 {
		return CountryData{}
	}

	sort.Slice(countries, func(i, j int) bool {
		return countries[i].Probability > countries[j].Probability
	})
	//round off country probability to 2 decimal places
	countries[0].Probability = roundTo(countries[0].Probability, 2)
	return countries[0]
}

func roundTo(num float64, places int) float64 {
	factor := math.Pow(10, float64(places))
	return math.Round(num*factor) / factor
}

func fetchDataFromAPI[T any](apiURL string, params string, w http.ResponseWriter) (T, error) {

	var result T

	fullURLPath := fmt.Sprintf("%v?name=%v", apiURL, url.QueryEscape(params))
	log.Printf("fetching data from url: %v...\n", fullURLPath)
	r, err := http.Get(fullURLPath)
	if err != nil {
		msg := fmt.Sprintf("%v returned an invalid response", apiURL)
		respondWithError(w, http.StatusBadGateway, msg)
		return result, errors.New(msg)
	}
	log.Printf("fetch from %v complete!\n", fullURLPath)

	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	err = decoder.Decode(&result)
	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		msg := "Upstream or server failure"
		respondWithError(w, r.StatusCode, msg)
		return result, errors.New(msg)
	}
	return result, nil
}

func parseReqBody(req *http.Request, format requestBody) (requestBody, error) {
	if err := json.NewDecoder(req.Body).Decode(&format); err != nil {
		return requestBody{}, err
	}
	return format, nil
}

func parseIntParam(r *http.Request, key string, defaultVal int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return defaultVal
	}
	return v
}

var isoCountryNames = map[string]string{
	"AF": "Afghanistan", "AL": "Albania", "DZ": "Algeria", "AR": "Argentina",
	"AU": "Australia", "AT": "Austria", "AZ": "Azerbaijan", "BD": "Bangladesh",
	"BE": "Belgium", "BR": "Brazil", "BG": "Bulgaria", "CA": "Canada",
	"CL": "Chile", "CN": "China", "CO": "Colombia", "HR": "Croatia",
	"CZ": "Czech Republic", "DK": "Denmark", "EG": "Egypt", "EE": "Estonia",
	"ET": "Ethiopia", "FI": "Finland", "FR": "France", "DE": "Germany",
	"GH": "Ghana", "GR": "Greece", "GT": "Guatemala", "HU": "Hungary",
	"IN": "India", "ID": "Indonesia", "IR": "Iran", "IQ": "Iraq",
	"IE": "Ireland", "IL": "Israel", "IT": "Italy", "JP": "Japan",
	"JO": "Jordan", "KZ": "Kazakhstan", "KE": "Kenya", "KR": "South Korea",
	"KW": "Kuwait", "LV": "Latvia", "LB": "Lebanon", "LT": "Lithuania",
	"MY": "Malaysia", "MX": "Mexico", "MA": "Morocco", "NL": "Netherlands",
	"NZ": "New Zealand", "NG": "Nigeria", "NO": "Norway", "PK": "Pakistan",
	"PE": "Peru", "PH": "Philippines", "PL": "Poland", "PT": "Portugal",
	"QA": "Qatar", "RO": "Romania", "RU": "Russia", "SA": "Saudi Arabia",
	"RS": "Serbia", "SG": "Singapore", "SK": "Slovakia", "ZA": "South Africa",
	"ES": "Spain", "SE": "Sweden", "CH": "Switzerland", "SY": "Syria",
	"TW": "Taiwan", "TZ": "Tanzania", "TH": "Thailand", "TN": "Tunisia",
	"TR": "Turkey", "UA": "Ukraine", "AE": "United Arab Emirates",
	"GB": "United Kingdom", "US": "United States", "UZ": "Uzbekistan",
	"VE": "Venezuela", "VN": "Vietnam", "YE": "Yemen", "ZW": "Zimbabwe",
}

func countryNameFromCode(code string) string {
	if name, ok := isoCountryNames[code]; ok {
		return name
	}
	return code
}

var countryNameToCode map[string]string

func init() {
	countryNameToCode = make(map[string]string, len(isoCountryNames))
	for code, name := range isoCountryNames {
		countryNameToCode[strings.ToLower(name)] = code
	}
}

func isValidIntParam(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func containsAny(words []string, targets []string) bool {
	for _, w := range words {
		for _, t := range targets {
			if w == t {
				return true
			}
		}
	}
	return false
}

func findWordIndex(words []string, targets []string) int {
	return slices.IndexFunc(words, func(w string) bool {
		return slices.Contains(targets, w)
	})
}

func findCountryCode(text string) string {
	words := strings.Fields(text)
	for length := len(words); length > 0; length-- {
		candidate := strings.Join(words[:length], " ")
		if code, ok := countryNameToCode[candidate]; ok {
			return code
		}
	}
	return ""
}

func parseNLQuery(q string) (database.ProfileFilter, bool) {
	words := strings.Fields(strings.ToLower(q))
	var filter database.ProfileFilter
	found := false

	hasMale := containsAny(words, []string{"male", "males", "man", "men", "boy", "boys"})
	hasFemale := containsAny(words, []string{"female", "females", "woman", "women", "girl", "girls"})
	if hasMale || hasFemale {
		found = true
		if hasMale && !hasFemale {
			g := "male"
			filter.Gender = &g
		} else if hasFemale && !hasMale {
			g := "female"
			filter.Gender = &g
		}
	}

	if containsAny(words, []string{"teenager", "teenagers", "teen", "teens"}) {
		ag := "teenager"
		filter.AgeGroup = &ag
		found = true
	} else if containsAny(words, []string{"adult", "adults"}) {
		ag := "adult"
		filter.AgeGroup = &ag
		found = true
	} else if containsAny(words, []string{"child", "children", "kid", "kids"}) {
		ag := "child"
		filter.AgeGroup = &ag
		found = true
	} else if containsAny(words, []string{"senior", "seniors", "elderly"}) {
		ag := "senior"
		filter.AgeGroup = &ag
		found = true
	}

	if containsAny(words, []string{"young"}) {
		found = true
		minAge := int32(16)
		maxAge := int32(24)
		filter.MinAge = &minAge
		filter.MaxAge = &maxAge
	}

	// "above/over X" overrides young's min_age
	if idx := findWordIndex(words, []string{"above", "over"}); idx >= 0 && idx+1 < len(words) {
		if n, err := strconv.Atoi(words[idx+1]); err == nil {
			v := int32(n)
			filter.MinAge = &v
			found = true
		}
	}

	// "below/under X" overrides young's max_age
	if idx := findWordIndex(words, []string{"below", "under"}); idx >= 0 && idx+1 < len(words) {
		if n, err := strconv.Atoi(words[idx+1]); err == nil {
			v := int32(n)
			filter.MaxAge = &v
			found = true
		}
	}

	if idx := findWordIndex(words, []string{"from"}); idx >= 0 && idx+1 < len(words) {
		remaining := strings.Join(words[idx+1:], " ")
		if code := findCountryCode(remaining); code != "" {
			filter.CountryID = &code
			found = true
		}
	}

	return filter, found
}
