// Package fortune makes fortune cookie slips: it picks what goes on one and
// draws it. The server does this rather than the page so that one call can
// print a slip with no browser involved, and the page shows the same slips
// from the same code.
package fortune

import (
	"math/rand/v2"
	"slices"
)

// Lesson is one thing to learn on a slip.
type Lesson struct {
	ZH     string `json:"zh"`
	Pinyin string `json:"pinyin"`
	EN     string `json:"en"`
	Note   string `json:"note,omitempty"`
}

// Slip is what a slip says.
type Slip struct {
	Fortune string `json:"fortune"`
	Numbers []int  `json:"numbers"`
	Lesson  Lesson `json:"lesson"`
}

// Pick chooses a whole slip's contents at random.
func Pick() Slip {
	return Slip{
		Fortune: fortunes[rand.IntN(len(fortunes))],
		Numbers: luckyNumbers(rand.IntN),
		Lesson:  lessons[rand.IntN(len(lessons))],
	}
}

// luckyNumbers are as on the real slips: six different ones from 1 to 49,
// smallest first. intN is rand.IntN's shape, for the tests.
func luckyNumbers(intN func(int) int) []int {
	picked := make([]int, 0, 6)
	for len(picked) < 6 {
		if n := 1 + intN(49); !slices.Contains(picked, n) {
			picked = append(picked, n)
		}
	}
	slices.Sort(picked)

	return picked
}
