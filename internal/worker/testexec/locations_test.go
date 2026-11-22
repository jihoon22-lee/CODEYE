package testexec

import (
	"strings"
	"testing"
)

func TestGoSubtestLocationsMatchRunnerNames(t *testing.T) {
	body := []byte(`package sample
import "testing"

func TestFormat(t *testing.T) {
    t.Run("good case", func(t *testing.T) {})
    t.Run("a\u00a0day\taway", func(t *testing.T) {})
    t.Run("non\x01print", func(t *testing.T) {})
    t.Run("same names", func(t *testing.T) {})
    t.Run("same_names", func(t *testing.T) {})
    t.Run("same names#01", func(t *testing.T) {})
    t.Run("only#01", func(t *testing.T) {})
    t.Run("long path", func(t *testing.T) {
        t.Run("end point", func(t *testing.T) {})
    })
    t.Run("shared/leaf", func(t *testing.T) {})
    t.Run("shared", func(t *testing.T) {
        t.Run("leaf", func(t *testing.T) {})
    })
    t.Run("", func(t *testing.T) {})
    t.Run("#00", func(t *testing.T) {})
}
`)
	for _, tc := range []struct {
		name string
		line int
	}{
		{"good_case", 5},
		{"a_day_away", 6},
		{`non\x01print`, 7},
		{"same_names", 0},
		{"same_names#01", 0},
		{"same_names#02", 0},
		{"only#01", 11},
		{"long_path/end_point", 13},
		{"shared/leaf", 0},
		{"#00", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := goSubtestLine(body, "TestFormat", strings.Split(tc.name, "/")); got != tc.line {
				t.Fatalf("location = %d, want %d", got, tc.line)
			}
		})
	}
}
