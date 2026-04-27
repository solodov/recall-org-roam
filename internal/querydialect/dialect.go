// Package querydialect layers org-search operators like is:overdue and due:today on top of raw Bleve query-string syntax.
package querydialect

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/blevesearch/bleve/v2"
	blevequery "github.com/blevesearch/bleve/v2/search/query"
)

// Compile turns one mixed org-search query into a Bleve query.
//
// Raw Bleve query-string syntax stays available for all unrecognized tokens.
// Recognized dialect operators are compiled into additional query filters and
// combined with the raw Bleve query through conjunction.
func Compile(raw string, now time.Time) (blevequery.Query, error) {
	parsed, err := parse(raw)
	if err != nil {
		return nil, err
	}

	filters := make([]blevequery.Query, 0, len(parsed.predicates))
	for _, predicate := range parsed.predicates {
		operator, ok := dialectOperators[predicate.operator]
		if !ok {
			continue
		}
		filter, err := operator.compile(predicate.value, now)
		if err != nil {
			return nil, err
		}
		filters = append(filters, filter)
	}

	baseQueryText := strings.TrimSpace(parsed.rawQueryText)
	switch {
	case baseQueryText != "" && len(filters) == 0:
		return bleve.NewQueryStringQuery(baseQueryText), nil
	case baseQueryText == "" && len(filters) == 0:
		return bleve.NewMatchNoneQuery(), nil
	case baseQueryText == "":
		return conjunction(filters), nil
	default:
		allQueries := make([]blevequery.Query, 0, len(filters)+1)
		allQueries = append(allQueries, bleve.NewQueryStringQuery(baseQueryText))
		allQueries = append(allQueries, filters...)
		return conjunction(allQueries), nil
	}
}

type parsedQuery struct {
	rawQueryText string
	predicates   []predicate
}

type predicate struct {
	operator string
	value    string
}

func parse(raw string) (parsedQuery, error) {
	tokens := scanTokens(raw)
	keptTokens := make([]string, 0, len(tokens))
	predicates := make([]predicate, 0)
	for _, token := range tokens {
		operatorName, value, hasOperator := strings.Cut(token, ":")
		if !hasOperator {
			keptTokens = append(keptTokens, token)
			continue
		}
		if _, isDialectOperator := dialectOperators[operatorName]; !isDialectOperator {
			keptTokens = append(keptTokens, token)
			continue
		}
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			return parsedQuery{}, fmt.Errorf("query operator %q requires a value", operatorName+":")
		}
		predicates = append(predicates, predicate{operator: operatorName, value: trimmedValue})
	}
	return parsedQuery{rawQueryText: strings.Join(keptTokens, " "), predicates: predicates}, nil
}

func scanTokens(raw string) []string {
	tokens := make([]string, 0)
	start := -1
	inQuotes := false
	escaped := false
	for index, runeValue := range raw {
		if start == -1 {
			if unicode.IsSpace(runeValue) {
				continue
			}
			start = index
		}
		if escaped {
			escaped = false
			continue
		}
		if inQuotes && runeValue == '\\' {
			escaped = true
			continue
		}
		if runeValue == '"' {
			inQuotes = !inQuotes
			continue
		}
		if !inQuotes && unicode.IsSpace(runeValue) {
			tokens = append(tokens, raw[start:index])
			start = -1
		}
	}
	if start != -1 {
		tokens = append(tokens, raw[start:])
	}
	return tokens
}

type dialectOperator interface {
	compile(value string, now time.Time) (blevequery.Query, error)
}

var dialectOperators = map[string]dialectOperator{
	"is":  isOperator{},
	"due": dueOperator{},
}

type isOperator struct{}

func (isOperator) compile(value string, now time.Time) (blevequery.Query, error) {
	switch value {
	case "overdue":
		return overdueQuery(now), nil
	default:
		return nil, fmt.Errorf("unsupported is: filter %q", value)
	}
}

type dueOperator struct{}

func (dueOperator) compile(value string, now time.Time) (blevequery.Query, error) {
	switch value {
	case "today":
		return dueTodayQuery(now), nil
	case "this-week":
		return dueThisWeekQuery(now), nil
	default:
		return nil, fmt.Errorf("unsupported due: filter %q", value)
	}
}

func overdueQuery(now time.Time) blevequery.Query {
	return conjunction([]blevequery.Query{
		notDoneQuery(),
		overdueCoreQuery(now),
	})
}

func dueTodayQuery(now time.Time) blevequery.Query {
	today := localDate(now)
	return conjunction([]blevequery.Query{
		notDoneQuery(),
		disjunction([]blevequery.Query{
			overdueCoreQuery(now),
			dateEqualsQuery("scheduled_date", today),
			dateEqualsQuery("deadline_date", today),
		}),
	})
}

func dueThisWeekQuery(now time.Time) blevequery.Query {
	weekStart, weekEnd := currentWeekBounds(now)
	return conjunction([]blevequery.Query{
		notDoneQuery(),
		disjunction([]blevequery.Query{
			overdueCoreQuery(now),
			dateRangeInclusiveQuery("scheduled_date", weekStart, weekEnd),
			dateRangeInclusiveQuery("deadline_date", weekStart, weekEnd),
		}),
	})
}

func overdueCoreQuery(now time.Time) blevequery.Query {
	today := localDate(now)
	currentMinute := float64(now.Hour()*60 + now.Minute())
	return disjunction([]blevequery.Query{
		dateBeforeQuery("scheduled_date", today),
		dateBeforeQuery("deadline_date", today),
		timedEarlierTodayQuery("scheduled_date", "scheduled_minute_of_day", today, currentMinute),
		timedEarlierTodayQuery("deadline_date", "deadline_minute_of_day", today, currentMinute),
	})
}

func timedEarlierTodayQuery(dateField string, minuteField string, today string, currentMinute float64) blevequery.Query {
	return conjunction([]blevequery.Query{
		dateEqualsQuery(dateField, today),
		numericBeforeQuery(minuteField, currentMinute),
	})
}

func dateBeforeQuery(field string, beforeDate string) blevequery.Query {
	dateQuery := bleve.NewTermRangeQuery("", beforeDate)
	dateQuery.SetField(field)
	return dateQuery
}

func dateEqualsQuery(field string, date string) blevequery.Query {
	dateQuery := bleve.NewTermQuery(date)
	dateQuery.SetField(field)
	return dateQuery
}

func dateRangeInclusiveQuery(field string, startDate string, endDate string) blevequery.Query {
	inclusive := true
	dateQuery := bleve.NewTermRangeInclusiveQuery(startDate, endDate, &inclusive, &inclusive)
	dateQuery.SetField(field)
	return dateQuery
}

func numericBeforeQuery(field string, max float64) blevequery.Query {
	numericQuery := bleve.NewNumericRangeQuery(nil, &max)
	numericQuery.SetField(field)
	return numericQuery
}

func notDoneQuery() blevequery.Query {
	doneBoolQuery := bleve.NewBoolFieldQuery(true)
	doneBoolQuery.SetField("is_done")
	doneTodoFallbackQuery := bleve.NewTermQuery("DONE")
	doneTodoFallbackQuery.SetField("todo")
	booleanQuery := bleve.NewBooleanQuery()
	booleanQuery.AddMustNot(disjunction([]blevequery.Query{doneBoolQuery, doneTodoFallbackQuery}))
	return booleanQuery
}

func conjunction(queries []blevequery.Query) blevequery.Query {
	if len(queries) == 1 {
		return queries[0]
	}
	return bleve.NewConjunctionQuery(queries...)
}

func disjunction(queries []blevequery.Query) blevequery.Query {
	if len(queries) == 1 {
		return queries[0]
	}
	disjunctionQuery := bleve.NewDisjunctionQuery(queries...)
	disjunctionQuery.SetMin(1)
	return disjunctionQuery
}

func localDate(now time.Time) string {
	return now.Format("2006-01-02")
}

func currentWeekBounds(now time.Time) (string, string) {
	weekdayOffset := (int(now.Weekday()) + 6) % 7
	startOfWeek := time.Date(now.Year(), now.Month(), now.Day()-weekdayOffset, 0, 0, 0, 0, now.Location())
	endOfWeek := startOfWeek.AddDate(0, 0, 6)
	return localDate(startOfWeek), localDate(endOfWeek)
}
