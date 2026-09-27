package ranking

import "strconv"

// Places and medals (SPEC_IOI §9.2, §10).

// Medal fractions of the official participants, cumulative: at most a
// twelfth get gold, a quarter gold or silver, half a medal (the IOI rule).
var medalShares = []struct {
	Medal string
	Share float64
}{{"gold", 1.0 / 12}, {"silver", 1.0 / 4}, {"bronze", 1.0 / 2}}

// Cutoff is the lowest total that still wins a medal, and how many do.
type Cutoff struct {
	Medal string  `json:"medal"`
	Score float64 `json:"score"`
	Count int     `json:"count"`
}

// ShownPlace is the place to display ("–" for unofficial and hidden rows).
func (r Row) ShownPlace() string {
	if r.Place == 0 {
		return "–"
	}
	return strconv.Itoa(r.Place)
}

// places numbers the official participants: rows are already in order,
// ties share a place, unofficial rows get none.
func (r *Ranking) places() {
	var prev *Row
	n := 0
	for i := range r.Rows {
		row := &r.Rows[i]
		row.Place, row.Medal = 0, ""
		if row.Unofficial {
			r.Unofficial = true
		}
		if row.Unofficial || row.Hidden {
			// Hidden participants (shown to administrators only) take no
			// place either, so every view numbers the others alike.
			continue
		}
		n++
		if prev != nil && r.cmp(prev, row) == 0 {
			row.Place = prev.Place
		} else {
			row.Place = n
		}
		prev = row
	}
}

// medals awards medals to the official, visible participants with points:
// for each medal the cumulative number of medallists is the largest that
// stays within its share without splitting a tie.
func (r *Ranking) medals() {
	var official []*Row
	for i := range r.Rows {
		if row := &r.Rows[i]; !row.Unofficial && !row.Hidden {
			official = append(official, row)
		}
	}
	r.Cutoffs = nil
	given := 0
	for _, m := range medalShares {
		limit := int(m.Share * float64(len(official)))
		end := given
		for end < len(official) && official[end].Total > 0 {
			// The whole tie group starting at end, or none of it.
			g := end + 1
			for g < len(official) && r.cmp(official[end], official[g]) == 0 {
				g++
			}
			if g > limit {
				break
			}
			end = g
		}
		for _, row := range official[given:end] {
			row.Medal = m.Medal
		}
		if end > given {
			r.Cutoffs = append(r.Cutoffs, Cutoff{Medal: m.Medal, Score: official[end-1].Total, Count: end - given})
		}
		given = end
	}
}

// Anonymize replaces the participants' names with their user ids, as the
// anonymized contest archive does (SPEC_IOI §14): results and countries
// stay, people do not.
func (r *Ranking) Anonymize() {
	for i := range r.Rows {
		row := &r.Rows[i]
		row.Username = "user" + strconv.FormatInt(row.UserID, 10)
		row.FirstName, row.LastName, row.Institution, row.Photo = "", "", "", ""
	}
}
