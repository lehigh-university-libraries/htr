// Package segeval evaluates explicitly represented OCR geometry without I/O.
package segeval

import (
	"bytes"
	"errors"
	"io"
	"math"
	"regexp"
	"slices"
	"sync"

	"github.com/lehigh-university-libraries/htr/pkg/ocrpage"
	yaml "go.yaml.in/yaml/v3"
)

// Measure identifies a primary or supporting metric.
type Measure string

// Supported measurements.
const (
	MeasurePRImA       Measure = "prima"
	MeasureCBAD        Measure = "cbad"
	MeasurePRImARegion Measure = "prima_region"
	MeasureMOTe        Measure = "mote"
	MeasureMatching    Measure = "matching"
	MeasureColumns     Measure = "columns"
)

// Weights assigns relative costs to layout errors and reading order.
type Weights struct {
	MergeH       float64 `yaml:"merge_h"`
	MergeV       float64 `yaml:"merge_v"`
	Split        float64 `yaml:"split"`
	Miss         float64 `yaml:"miss"`
	Partial      float64 `yaml:"partial"`
	FalseDet     float64 `yaml:"false_detection"`
	Misclass     float64 `yaml:"misclassification"`
	ReadingOrder float64 `yaml:"reading_order"`
}

// Profile describes the measurement policy for one document type.
type Profile struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	Level       ocrpage.Level        `yaml:"level"`
	Primary     Measure              `yaml:"primary"`
	Fallback    Measure              `yaml:"fallback"`
	Tolerance   float64              `yaml:"tolerance"`
	Weights     Weights              `yaml:"weights"`
	Ignore      []ocrpage.RegionType `yaml:"ignore"`
	Supporting  []Measure            `yaml:"supporting"`
	Calibrated  bool                 `yaml:"calibrated"`
}

var registry = struct {
	sync.RWMutex
	m map[string]Profile
}{m: map[string]Profile{}}
var profileName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func clone(p Profile) Profile {
	p.Ignore = slices.Clone(p.Ignore)
	p.Supporting = slices.Clone(p.Supporting)
	return p
}
func validProfile(p Profile) error {
	if !profileName.MatchString(p.Name) || ocrpage.CheckLevel(p.Level) != nil || p.Tolerance <= 0 || p.Tolerance >= 1 {
		return errors.New("segeval: invalid profile")
	}
	if p.Primary != MeasurePRImA && p.Primary != MeasureCBAD && p.Primary != MeasurePRImARegion {
		return errors.New("segeval: invalid primary measure")
	}
	if p.Primary == MeasureCBAD && p.Fallback != MeasurePRImA {
		return errors.New("segeval: cBAD requires prima fallback")
	}
	total := 0.0
	for _, w := range p.Weights.values() {
		if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
			return errors.New("segeval: invalid weight")
		}
		total += w
	}
	if total == 0 || math.IsInf(total, 0) || math.IsNaN(p.Tolerance) {
		return errors.New("segeval: invalid profile")
	}
	for _, m := range p.Supporting {
		if !slices.Contains([]Measure{MeasurePRImA, MeasureCBAD, MeasurePRImARegion, MeasureMOTe, MeasureMatching, MeasureColumns}, m) {
			return errors.New("segeval: invalid supporting measure")
		}
	}
	return nil
}
func (w Weights) values() []float64 {
	return []float64{w.MergeH, w.MergeV, w.Split, w.Miss, w.Partial, w.FalseDet, w.Misclass, w.ReadingOrder}
}

// Register adds a profile, rejecting duplicates and invalid values.
func Register(p Profile) error {
	if err := validProfile(p); err != nil {
		return err
	}
	registry.Lock()
	defer registry.Unlock()
	if _, ok := registry.m[p.Name]; ok {
		return errors.New("segeval: profile already registered")
	}
	registry.m[p.Name] = clone(p)
	return nil
}

// Lookup returns an independent copy of a registered profile.
func Lookup(name string) (Profile, bool) {
	registry.RLock()
	defer registry.RUnlock()
	p, ok := registry.m[name]
	return clone(p), ok
}

// Profiles lists registered policies alphabetically.
func Profiles() []Profile {
	registry.RLock()
	defer registry.RUnlock()
	v := make([]Profile, 0, len(registry.m))
	for _, p := range registry.m {
		v = append(v, clone(p))
	}
	slices.SortFunc(v, func(a, b Profile) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return v
}

// LoadProfile loads a bounded YAML or JSON policy, rejecting unknown fields.
func LoadProfile(r io.Reader) (Profile, error) {
	b, e := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if e != nil {
		return Profile{}, e
	}
	if len(b) > 1<<20 {
		return Profile{}, ocrpage.ErrLimit
	}
	var p Profile
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if e = d.Decode(&p); e != nil {
		return Profile{}, e
	}
	var trailing any
	if e = d.Decode(&trailing); e != io.EOF {
		return Profile{}, errors.New("segeval: expected one profile document")
	}
	if p.Level == "" {
		p.Level = ocrpage.LevelLine
	}
	if p.Tolerance == 0 {
		p.Tolerance = .1
	}
	if e = validProfile(p); e != nil {
		return Profile{}, e
	}
	return p, nil
}
func init() {
	for _, p := range []Profile{
		{Name: "generic", Description: "General layout", Weights: Weights{1, 1, 1, 1, 1, 1, 1, 1}},
		{Name: "printed", Description: "Printed pages", Weights: Weights{1, 1, .5, 1, .75, .5, .5, .5}},
		{Name: "newspaper", Description: "Ordered multicolumn pages", Weights: Weights{2, .5, .25, 1, .5, .25, .5, 1}, Ignore: []ocrpage.RegionType{ocrpage.RegionImage, ocrpage.RegionAdvert}, Supporting: []Measure{MeasurePRImARegion}},
		{Name: "manuscript", Description: "Handwritten manuscripts", Primary: MeasureCBAD, Fallback: MeasurePRImA, Weights: Weights{1, 1, .5, 1, .75, .25, .25, .5}, Ignore: []ocrpage.RegionType{ocrpage.RegionNoise}},
		{Name: "letter", Description: "Handwritten correspondence", Primary: MeasureCBAD, Fallback: MeasurePRImA, Weights: Weights{1, 1, .5, 1, 1, .5, .25, .5}},
		{Name: "ledger", Description: "Column-sensitive account records", Weights: Weights{2, .5, .5, 1.5, .75, .5, .25, .25}, Ignore: []ocrpage.RegionType{ocrpage.RegionSeparator}, Supporting: []Measure{MeasureColumns}},
		{Name: "form", Description: "Labels and fields", Weights: Weights{2, .5, .5, 1.5, .75, .25, .25, .1}, Ignore: []ocrpage.RegionType{ocrpage.RegionSeparator}, Supporting: []Measure{MeasureColumns}},
	} {
		p.Level = ocrpage.LevelLine
		p.Tolerance = .1
		if p.Primary == "" {
			p.Primary = MeasurePRImA
		}
		p.Supporting = append(p.Supporting, MeasureMOTe, MeasureMatching)
		if err := Register(p); err != nil {
			panic(err)
		}
	}
}
