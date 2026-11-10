// Package builds serves ARAM Mayhem item builds and augment statistics from
// community data sites (aramkit, aramgg, op.gg).
package builds

import (
	"strings"

	"github.com/aarlint/ezlol/internal/ddragon"
)

// Count is a games/wins tally.
type Count struct {
	Games int `json:"games"`
	Wins  int `json:"wins"`
}

// ItemRef is an item with display data.
type ItemRef struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
}

// ItemSet is a group of items (a starter set, a core build, one boots or late item) and its record.
type ItemSet struct {
	Count
	Items []ItemRef `json:"items"`
}

// Build is what the UI shows for a champion in ARAM Mayhem.
type Build struct {
	Champion ddragon.Champion `json:"champion"`
	Patch    string           `json:"patch"`
	Mode     string           `json:"mode"`   // always "aram"
	Source   string           `json:"source"` // aramkit | opgg | none
	Total    Count            `json:"total"`
	Starting []ItemSet        `json:"starting"`
	Core     []ItemSet        `json:"core"`
	CoreTags []string         `json:"coreTags,omitempty"` // build style of each core set (aramkit): crit, ap, …
	Boots    []ItemSet        `json:"boots"`
	Late     []ItemSet        `json:"late"`
	Tier     string           `json:"tier,omitempty"` // S A B C D
	Rank     int              `json:"rank,omitempty"`
	PickRate float64          `json:"pickRate,omitempty"`
	Augments []Augment        `json:"augments,omitempty"`
	AugScope string           `json:"augScope,omitempty"`  // champion | global
	AugFrom  string           `json:"augSource,omitempty"` // aramkit | aramgg
	Notes    []string         `json:"notes"`
}

// ShortPatch maps "16.17.8104348" to "16.17".
func ShortPatch(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

// QueueTag groups queue ids: "aram" for ARAM and ARAM Mayhem, "other" for the rest.
func QueueTag(queueID int) string {
	switch queueID {
	case 450, 2400, 100: // ARAM, ARAM Mayhem, Butcher's Bridge
		return "aram"
	default:
		return "other"
	}
}
