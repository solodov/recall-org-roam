package querydialect

import (
	"reflect"
	"testing"
	"time"
)

func TestParseKeepsRawBleveTokensAndExtractsDialectPredicates(t *testing.T) {
	t.Helper()

	parsed, err := parse(`headline:alpha "exact phrase" is:overdue todo:TODO is:due-today`)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if got, want := parsed.rawQueryText, `headline:alpha "exact phrase" todo:TODO`; got != want {
		t.Fatalf("rawQueryText = %q, want %q", got, want)
	}
	if got, want := parsed.predicates, []predicate{{operator: "is", value: "overdue"}, {operator: "is", value: "due-today"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("predicates = %#v, want %#v", got, want)
	}
}

func TestParseRejectsMissingDialectOperatorValues(t *testing.T) {
	t.Helper()

	_, err := parse(`is:`)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCompileRejectsUnknownIsFilters(t *testing.T) {
	t.Helper()

	_, err := Compile(`is:tomorrow`, time.Date(2026, time.April, 29, 10, 30, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected compile error")
	}
}

func TestCurrentWeekBoundsUseMondayThroughSunday(t *testing.T) {
	t.Helper()

	weekStart, weekEnd := currentWeekBounds(time.Date(2026, time.April, 29, 10, 30, 0, 0, time.UTC))
	if got, want := weekStart, "2026-04-27"; got != want {
		t.Fatalf("weekStart = %q, want %q", got, want)
	}
	if got, want := weekEnd, "2026-05-03"; got != want {
		t.Fatalf("weekEnd = %q, want %q", got, want)
	}
}
