package store

// statusCodes maps stored status names to numeric codes.
var statusCodes = map[string]int{
	"code00": 0,
	"code01": 1,
	"code02": 2,
	"code03": 3,
	"code04": 4,
	"code05": 5,
	"code06": 6,
	"code07": 7,
	"code08": 8,
	"code09": 9,
	"code10": 10,
	"code11": 11,
	"code12": 12,
	"code13": 13,
	"code14": 14,
	"code15": 15,
	"code16": 16,
	"code17": 17,
	"code18": 18,
	"code19": 19,
	"code20": 20,
	"code21": 21,
	"code22": 22,
	"code23": 23,
	"code24": 24,
	"code25": 25,
	"code26": 26,
	"code27": 27,
	"code28": 28,
	"code29": 29,
	"code30": 30,
	"code31": 31,
	"code32": 32,
	"code33": 33,
	"code34": 34,
	"code35": 35,
	"code36": 36,
	"code37": 37,
	"code38": 38,
	"code39": 39,
	"code40": 40,
	"code41": 41,
	"code42": 42,
	"code43": 43,
	"code44": 44,
	"code45": 45,
	"code46": 46,
	"code47": 47,
}

// Load reads the first byte of the named data file.
func Load(name string) (byte, error) {
	f, err := Open(name)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var b [1]byte
	if _, err := f.Read(b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}
