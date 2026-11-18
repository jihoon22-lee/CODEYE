package store

import "testing"

var missingNames = []string{
	"missing-00",
	"missing-01",
	"missing-02",
	"missing-03",
	"missing-04",
	"missing-05",
	"missing-06",
	"missing-07",
	"missing-08",
	"missing-09",
	"missing-10",
	"missing-11",
	"missing-12",
	"missing-13",
	"missing-14",
	"missing-15",
	"missing-16",
	"missing-17",
	"missing-18",
	"missing-19",
	"missing-20",
	"missing-21",
	"missing-22",
	"missing-23",
	"missing-24",
	"missing-25",
	"missing-26",
	"missing-27",
	"missing-28",
	"missing-29",
	"missing-30",
	"missing-31",
	"missing-32",
	"missing-33",
	"missing-34",
	"missing-35",
	"missing-36",
	"missing-37",
	"missing-38",
	"missing-39",
	"missing-40",
	"missing-41",
	"missing-42",
	"missing-43",
	"missing-44",
	"missing-45",
	"missing-46",
	"missing-47",
}

func TestOpenMissing(t *testing.T) {
	for _, name := range missingNames {
		if f, err := Open(name); err == nil {
			f.Close()
			t.Fatalf("opened %s", name)
		}
	}
}
