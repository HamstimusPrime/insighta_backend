package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"insighta_backend/internal/database"
	"insighta_backend/internal/middleware"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func buildPaginationLinks(base string, page, limit, total int) PaginationLinks {
	totalPages := int(math.Ceil(float64(total) / float64(limit)))
	self := base + "?page=" + strconv.Itoa(page) + "&limit=" + strconv.Itoa(limit)
	links := PaginationLinks{Self: self}
	if page < totalPages {
		next := base + "?page=" + strconv.Itoa(page+1) + "&limit=" + strconv.Itoa(limit)
		links.Next = &next
	}
	if page > 1 {
		prev := base + "?page=" + strconv.Itoa(page-1) + "&limit=" + strconv.Itoa(limit)
		links.Prev = &prev
	}
	return links
}

func (cfg *apiConfig) handlerGetCurrentUser(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respondWithError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	dbUser, err := cfg.db.GetAuthUserByID(r.Context(), claims.UserID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	respondWithJSON(w, map[string]interface{}{
		"user_id":    claims.UserID,
		"username":   claims.Username,
		"role":       claims.Role,
		"email":      dbUser.Email,
		"avatar_url": dbUser.AvatarUrl,
	}, http.StatusOK)
}

func handlerCreateProfile(w http.ResponseWriter, r *http.Request, q *database.Queries) {

	w.Header().Set("Access-Control-Allow-Origin", "*")

	reqBody, err := parseReqBody(r, requestBody{})
	if err != nil {
		log.Printf("unable to parse request body, err: %v\n", err)
		respondWithError(w, 502, " Upstream or server failure")
		return
	}

	if reqBody.Name == "" {
		respondWithError(w, 422, " Unprocessable Entity: Invalid type")
		return
	}

	var dbUser database.User
	var createUserObj userProfile

	dbUser, err = q.GetProfileByName(context.Background(), reqBody.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// continue to create profile with name if name not found in DB
			log.Println("user does not exist, continuing...")
		} else {
			// Real error — stop execution
			log.Printf("error fetching profile by name err: %v", err)
			respondWithError(w, 500, "internal server Error")
			return
		}
	}

	if dbUser.Name == reqBody.Name {
		log.Printf("duplicate entry! entry with name: {%v} already exists!!\n", reqBody.Name)

		createUserObj.Status = "success"
		createUserObj.Message = "Profile already exists"
		createUserObj.Data = userData{
			ID:                 dbUser.ID,
			Name:               dbUser.Name,
			Gender:             dbUser.Gender,
			GenderProbability:  dbUser.GenderProbability,
			Age:                int(dbUser.Age),
			AgeGroup:           dbUser.AgeGroup,
			CountryID:          dbUser.CountryID,
			CountryProbability: dbUser.CountryProbability,
		}

		respondWithJSON(w, createUserObj, 200)
		return
	}

	//--- fetch genderizeAPI ---
	genderizeData, err := fetchDataFromAPI[GenderizeResponse](GENDERIZE_API_URL, reqBody.Name, w)
	if err != nil {
		return
	}
	//if Genderize returns gender: null or count: 0 → return 502, do not store
	if (genderizeData.Gender == "") || (genderizeData.Count == 0) {
		respondWithError(w, 502, fmt.Sprintf("%v returned an invalid response", GENDERIZE_API_URL))
		return
	}

	//--- fetch agifyAPI ---
	agifyData, err := fetchDataFromAPI[AgifyResponse](AGIFY_API_URL, reqBody.Name, w)
	if err != nil {
		return
	}
	// If Agify returns age: null → return 502, do not store
	if agifyData.Age == 0 {
		respondWithError(w, 502, fmt.Sprintf("%v returned an invalid response", AGIFY_API_URL))
		return
	}

	//--- fetch nationalizeAPI ---
	nationalizeData, err := fetchDataFromAPI[NationalizeResponse](NATIONALIZE_API_URL, reqBody.Name, w)
	if err != nil {
		return
	}
	// If Nationalize returns no country data → return 502, do not store
	if len(nationalizeData.Country) == 0 {
		respondWithError(w, 502, fmt.Sprintf("%v returned an invalid response", AGIFY_API_URL))
		return
	}

	// Nationality: pick the country with
	// the highest probability from the Nationalize response

	profile := database.CreateProfileParams{
		ID:                 uuid.New(),
		Name:               genderizeData.Name,
		Gender:             genderizeData.Gender,
		GenderProbability:  genderizeData.Probability,
		Age:                int32(agifyData.Age),
		AgeGroup:           ageGroupFromAgify(agifyData.Age),
		CountryID:          getTopCountry(nationalizeData.Country).CountryID,
		CountryProbability: getTopCountry(nationalizeData.Country).Probability,
	}

	dbUser, err = q.CreateProfile(context.Background(), profile)
	if err != nil {
		log.Printf("error creating profile, error: %v", err)
		respondWithError(w, 500, "internal server Error")
		return
	}

	createUserObj.Status = "success"
	createUserObj.Data = userData{
		ID:                 dbUser.ID,
		Name:               dbUser.Name,
		Gender:             dbUser.Gender,
		GenderProbability:  dbUser.GenderProbability,
		Age:                int(dbUser.Age),
		AgeGroup:           dbUser.AgeGroup,
		CountryID:          dbUser.CountryID,
		CountryProbability: dbUser.CountryProbability,
	}

	respondWithJSON(w, createUserObj, 201)
	return
}

func handlerGetProfileWithID(w http.ResponseWriter, r *http.Request, q *database.Queries) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	userInput := chi.URLParam(r, "id")
	id, err := uuid.Parse(userInput)
	if err != nil {
		log.Printf("error, ID: %v is not a valid UUID", userInput)
		respondWithError(w, 422, "Unprocessable Entity: Invalid type")
		return
	}
	log.Printf("profile id with value: %v is a valid UUID\n", userInput)

	profileFromDB, err := q.GetProfileByID(context.Background(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No user found
			log.Println("profile does not exist")
			respondWithError(w, 404, "Not Found: Profile not found")
			return
		} else {
			// Real error — stop execution
			log.Printf("error fetching user by name err: %v", err)
			respondWithError(w, 500, "internal server Error")
			return
		}
	}

	var profileObj userProfile
	profileObj.Status = "success"
	profileObj.Data = userData{
		ID:                 profileFromDB.ID,
		Name:               profileFromDB.Name,
		Gender:             profileFromDB.Gender,
		GenderProbability:  profileFromDB.GenderProbability,
		Age:                int(profileFromDB.Age),
		AgeGroup:           profileFromDB.AgeGroup,
		CountryID:          profileFromDB.CountryID,
		CountryProbability: profileFromDB.CountryProbability,
	}

	respondWithJSON(w, profileObj, 200)
	return

}

func handlerGetProfiles(w http.ResponseWriter, r *http.Request, q *database.Queries) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	page := parseIntParam(r, "page", 1)
	limit := parseIntParam(r, "limit", 10)
	if limit > 50 {
		limit = 50
	}
	offset := (page - 1) * limit

	//initialize filtered profiles and first assign default values to it
	filter := database.ProfileFilter{
		Limit:  int32(limit),
		Offset: int32(offset),
	}

	// get accepted queries from request if provided, but default to
	// empty string or zero value if not provided. Use them to build
	//the ProfileFilter struct
	if gender := r.URL.Query().Get("gender"); gender != "" {
		filter.Gender = &gender
	}
	if countryID := r.URL.Query().Get("country_id"); countryID != "" {
		filter.CountryID = &countryID
	}
	if ageGroup := r.URL.Query().Get("age_group"); ageGroup != "" {
		filter.AgeGroup = &ageGroup
	}
	if minAge := parseIntParam(r, "min_age", 0); minAge > 0 {
		v := int32(minAge)
		filter.MinAge = &v
	}
	if maxAge := parseIntParam(r, "max_age", 0); maxAge > 0 {
		v := int32(maxAge)
		filter.MaxAge = &v
	}

	// map of all possible parameters that we could sort our DB columns by
	allowedSortCols := map[string]bool{
		"name": true, "age": true, "gender": true,
		"country_id": true, "gender_probability": true,
		"country_probability": true, "created_at": true,
	}

	//get sortby and order filter value if provided
	if sortBy := r.URL.Query().Get("sort_by"); allowedSortCols[sortBy] {
		filter.SortBy = sortBy
	}
	//convert values for SQL Query compatibility
	order := r.URL.Query().Get("order")
	if order == "desc" || order == "DESC" {
		filter.Order = "DESC"
	} else {
		filter.Order = "ASC"
	}

	//get the total number of profiles that match all our request from the DB
	total, err := q.GetFilteredProfileCount(context.Background(), filter)
	if err != nil {
		log.Printf("error counting profiles: %v", err)
		respondWithError(w, 500, "internal server error")
		return
	}

	profilesFromDB, err := q.GetFilteredProfiles(context.Background(), filter)
	if err != nil {
		log.Printf("error fetching profiles: %v", err)
		respondWithError(w, 500, "internal server error")
		return
	}

	var profiles []allProfiilesData
	for _, p := range profilesFromDB {
		profiles = append(profiles, allProfiilesData{
			ID:                 p.ID,
			Name:               p.Name,
			Gender:             p.Gender,
			GenderProbability:  p.GenderProbability,
			Age:                int(p.Age),
			AgeGroup:           p.AgeGroup,
			CountryID:          p.CountryID,
			CountryName:        countryNameFromCode(p.CountryID),
			CountryProbability: p.CountryProbability,
		})
	}

	totalPages := int(math.Ceil(float64(total) / float64(limit)))
	respondWithJSON(w, pagedProfilesResponse{
		Status:     "success",
		Page:       page,
		Limit:      limit,
		Total:      total,
		TotalPages: totalPages,
		Links:      buildPaginationLinks("/api/profiles", page, limit, total),
		Data:       profiles,
	}, 200)
}

func handlerDeleteProfileWithID(w http.ResponseWriter, r *http.Request, q *database.Queries) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	userInput := chi.URLParam(r, "id")
	id, err := uuid.Parse(userInput)
	if err != nil {
		errorMsg := fmt.Sprintf("error, ID: %v is not a valid UUID", userInput)
		respondWithError(w, 400, errorMsg)
		return
	}

	err = q.DeleteProfileByID(context.Background(), id)
	if err != nil {
		respondWithError(w, 500, "internal server Error")
		return
	}
	w.WriteHeader(204)

}

func handlerNLQsearch(w http.ResponseWriter, r *http.Request, q *database.Queries) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	query := r.URL.Query().Get("q")
	if strings.TrimSpace(query) == "" {
		respondWithError(w, 400, "Missing or empty parameter")
		return
	}

	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")
	if (pageStr != "" && !isValidIntParam(pageStr)) || (limitStr != "" && !isValidIntParam(limitStr)) {
		respondWithError(w, 422, "Invalid query parameters")
		return
	}

	filter, ok := parseNLQuery(query)
	if !ok {
		respondWithError(w, 422, "Unable to interpret query")
		return
	}

	page := parseIntParam(r, "page", 1)
	limit := parseIntParam(r, "limit", 10)
	if limit > 50 {
		limit = 50
	}
	offset := (page - 1) * limit
	filter.Limit = int32(limit)
	filter.Offset = int32(offset)
	filter.Order = "ASC"

	total, err := q.GetFilteredProfileCount(context.Background(), filter)
	if err != nil {
		log.Printf("error counting profiles: %v", err)
		respondWithError(w, 500, "internal server error")
		return
	}

	if total == 0 {
		respondWithError(w, 404, "Profile not found")
		return
	}

	profilesFromDB, err := q.GetFilteredProfiles(context.Background(), filter)
	if err != nil {
		log.Printf("error fetching profiles: %v", err)
		respondWithError(w, 500, "internal server error")
		return
	}

	var profiles []allProfiilesData
	for _, p := range profilesFromDB {
		profiles = append(profiles, allProfiilesData{
			ID:                 p.ID,
			Name:               p.Name,
			Gender:             p.Gender,
			GenderProbability:  p.GenderProbability,
			Age:                int(p.Age),
			AgeGroup:           p.AgeGroup,
			CountryID:          p.CountryID,
			CountryName:        countryNameFromCode(p.CountryID),
			CountryProbability: p.CountryProbability,
		})
	}

	totalPages := int(math.Ceil(float64(total) / float64(limit)))
	respondWithJSON(w, pagedProfilesResponse{
		Status:     "success",
		Page:       page,
		Limit:      limit,
		Total:      total,
		TotalPages: totalPages,
		Links:      buildPaginationLinks("/api/profiles/search", page, limit, total),
		Data:       profiles,
	}, 200)
}

func handlerExportProfiles(w http.ResponseWriter, r *http.Request, q *database.Queries) {
	if r.URL.Query().Get("format") != "csv" {
		respondWithError(w, http.StatusBadRequest, "unsupported format; use ?format=csv")
		return
	}

	filter := database.ProfileFilter{
		Limit:  10000,
		Offset: 0,
		Order:  "ASC",
	}
	if gender := r.URL.Query().Get("gender"); gender != "" {
		filter.Gender = &gender
	}
	if countryID := r.URL.Query().Get("country_id"); countryID != "" {
		filter.CountryID = &countryID
	}
	if ageGroup := r.URL.Query().Get("age_group"); ageGroup != "" {
		filter.AgeGroup = &ageGroup
	}
	if minAge := parseIntParam(r, "min_age", 0); minAge > 0 {
		v := int32(minAge)
		filter.MinAge = &v
	}
	if maxAge := parseIntParam(r, "max_age", 0); maxAge > 0 {
		v := int32(maxAge)
		filter.MaxAge = &v
	}
	allowedSortCols := map[string]bool{
		"name": true, "age": true, "gender": true,
		"country_id": true, "gender_probability": true,
		"country_probability": true, "created_at": true,
	}
	if sortBy := r.URL.Query().Get("sort_by"); allowedSortCols[sortBy] {
		filter.SortBy = sortBy
	}
	if order := r.URL.Query().Get("order"); order == "desc" || order == "DESC" {
		filter.Order = "DESC"
	}

	profiles, err := q.GetFilteredProfiles(context.Background(), filter)
	if err != nil {
		log.Printf("export profiles error: %v", err)
		respondWithError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	filename := "profiles_" + time.Now().UTC().Format("20060102T150405Z") + ".csv"
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	cw := csv.NewWriter(w)
	cw.Write([]string{"id", "name", "gender", "gender_probability", "age", "age_group", "country_id", "country_name", "country_probability", "created_at"})
	for _, p := range profiles {
		cw.Write([]string{
			p.ID.String(),
			p.Name,
			p.Gender,
			strconv.FormatFloat(p.GenderProbability, 'f', -1, 64),
			strconv.Itoa(int(p.Age)),
			p.AgeGroup,
			p.CountryID,
			countryNameFromCode(p.CountryID),
			strconv.FormatFloat(p.CountryProbability, 'f', -1, 64),
			p.CreatedAt.String,
		})
	}
	cw.Flush()
}
