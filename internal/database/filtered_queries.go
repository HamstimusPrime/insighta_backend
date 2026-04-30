package database

import (
	"context"
	"fmt"
	"strings"
)

type ProfileFilter struct {
	Gender                *string
	AgeGroup              *string
	CountryID             *string
	MinAge                *int32
	MaxAge                *int32
	MinGenderProbability  *float64
	MinCountryProbability *float64
	SortBy                string
	Order                 string
	Limit                 int32
	Offset                int32
}

func buildWhereClause(f ProfileFilter) (string, []any) {
	var sb strings.Builder
	var args []any
	paramCount := 1
	whereStarted := false

	addCondition := func(clause string, val any) {
		if !whereStarted {
			sb.WriteString(" WHERE ")
			whereStarted = true
		} else {
			sb.WriteString(" AND ")
		}
		sb.WriteString(fmt.Sprintf(clause, paramCount))
		args = append(args, val)
		paramCount++
	}

	if f.Gender != nil {
		addCondition("gender = $%d", *f.Gender)
	}
	if f.AgeGroup != nil {
		addCondition("age_group = $%d", *f.AgeGroup)
	}
	if f.CountryID != nil {
		addCondition("country_id = $%d", *f.CountryID)
	}
	if f.MinAge != nil {
		addCondition("age >= $%d", *f.MinAge)
	}
	if f.MaxAge != nil {
		addCondition("age <= $%d", *f.MaxAge)
	}
	if f.MinGenderProbability != nil {
		addCondition("gender_probability >= $%d", *f.MinGenderProbability)
	}
	if f.MinCountryProbability != nil {
		addCondition("country_probability >= $%d", *f.MinCountryProbability)
	}
	return sb.String(), args
}

func (q *Queries) GetFilteredProfiles(ctx context.Context, f ProfileFilter) ([]User, error) {
	//the buildWhereClause funciton takes a ProfileFilter struct and uses its values to
	//to generate the 'WHERE' section of the Query to the DB
	//the rest of the function genereates the other sections of the query
	where, args := buildWhereClause(f)
	paramCount := len(args) + 1

	var sb strings.Builder
	sb.WriteString(`SELECT id, name, gender, gender_probability, age, age_group, country_id, country_probability, created_at FROM users`)
	sb.WriteString(where)
	if f.SortBy != "" {
		sb.WriteString(" ORDER BY " + f.SortBy + " " + f.Order)
	}
	sb.WriteString(fmt.Sprintf(" LIMIT $%d OFFSET $%d", paramCount, paramCount+1))
	args = append(args, f.Limit, f.Offset)

	rows, err := q.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []User
	for rows.Next() {
		var i User
		if err := rows.Scan(
			&i.ID,
			&i.Name,
			&i.Gender,
			&i.GenderProbability,
			&i.Age,
			&i.AgeGroup,
			&i.CountryID,
			&i.CountryProbability,
			&i.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func (q *Queries) GetFilteredProfileCount(ctx context.Context, f ProfileFilter) (int, error) {
	where, args := buildWhereClause(f)
	query := "SELECT COUNT(*) FROM users" + where
	var count int
	err := q.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}
