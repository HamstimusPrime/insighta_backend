package main

import (
	"time"

	"github.com/google/uuid"
)

type allProfiiles struct {
	Status string             `json:"status"`
	Page   int                `json:"page"`
	Limit  int                `json:"limit"`
	Total  int                `json:"total"`
	Data   []allProfiilesData `json:"data"`
}

type allProfiilesData struct {
	ID                 uuid.UUID `json:"id"`
	Name               string    `json:"name"`
	Gender             string    `json:"gender"`
	GenderProbability  float64   `json:"gender_probability"`
	Age                int       `json:"age"`
	AgeGroup           string    `json:"age_group"`
	CountryID          string    `json:"country_id"`
	CountryName        string    `json:"country_name"`
	CountryProbability float64   `json:"country_probability"`
	CreatedAt          time.Time `json:"created_at"`
}

type userProfile struct {
	Status  string   `json:"status"`
	Message string   `json:"message,omitempty"`
	Data    userData `json:"data"`
}

type userData struct {
	ID                 uuid.UUID `json:"id"`
	Name               string    `json:"name"`
	Gender             string    `json:"gender"`
	GenderProbability  float64   `json:"gender_probability"`
	Age                int       `json:"age"`
	AgeGroup           string    `json:"age_group"`
	CountryID          string    `json:"country_id"`
	CountryProbability float64   `json:"country_probability"`
	CreatedAt          time.Time `json:"created_at"`
}

type PaginationLinks struct {
	Self string  `json:"self"`
	Next *string `json:"next"`
	Prev *string `json:"prev"`
}

type pagedProfilesResponse struct {
	Status     string             `json:"status"`
	Page       int                `json:"page"`
	Limit      int                `json:"limit"`
	Total      int                `json:"total"`
	TotalPages int                `json:"total_pages"`
	Links      PaginationLinks    `json:"links"`
	Data       []allProfiilesData `json:"data"`
}

type ErrorObject struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code,omitempty"`
}

type NationalizeResponse struct {
	Count   int           `json:"count"`
	Name    string        `json:"name"`
	Country []CountryData `json:"country"`
}

type CountryData struct {
	CountryID   string  `json:"country_id"`
	Probability float64 `json:"probability"`
}

type AgifyResponse struct {
	Count int    `json:"count"`
	Name  string `json:"name"`
	Age   int    `json:"age"`
}

type GenderizeResponse struct {
	Count       int     `json:"count"`
	Name        string  `json:"name"`
	Gender      string  `json:"gender"`
	Probability float64 `json:"probability"`
}

type requestBody struct {
	Name string `json:"name"`
}

type AuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Username     string `json:"username"`
	Role         string `json:"role"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}
