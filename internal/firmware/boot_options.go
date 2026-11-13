package firmware

// Compatibility is deliberately independent of the frontends. Both frontends
// and the privileged writer evaluate this same table, including catalogue rows.
import (
	"errors"
	"fmt"
	"strings"
)

const (
	BootOptionsTitle           = "Advanced boot options"
	BootOptionsHint            = "For consoles that don't start or don't wake up. Leave these alone otherwise."
	BootChainWarning           = "Test builds. If the console doesn't start, flash again with \"Standard\"."
	BootOptionsConflict        = "These options don't work together"
	BootOptionsConflictBody    = "%s\nThe console will probably not start or not wake up with this card."
	BootOptionsUntestedConsole = "Not tested on this console yet."
	BootOptionsUntestedLoader  = "Not tested with this bootloader yet."
	BootOptionsEmbedded        = "Part of the mainline chain"
)

type BootWorksWith struct {
	Chains      []string `json:"chains"`
	Bootloaders []string `json:"bootloaders"`
	KernelLines []string `json:"kernel_lines"`
	Profiles    []string `json:"profiles"`
	Storage     []string `json:"storage"`
}
type BootRule struct {
	Kind              string        `json:"kind"` // bootloader, sleep, chain
	ID                string        `json:"id"`
	Label             string        `json:"label_key"`
	WorksWith         BootWorksWith `json:"works_with"`
	TestedOn          []string      `json:"tested_on"`
	TestedBootloaders []string      `json:"tested_bootloaders"`
	// A restriction is physically impossible; an explicit broken pair can be overridden.
	Broken []BootBrokenPair `json:"known_broken,omitempty"`
}
type BootBrokenPair struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}
type BootTarget struct {
	KernelLine     string `json:"kernel_line"`
	Profile        string `json:"profile"`
	Console        string `json:"console"`
	Storage        string `json:"storage"`
	CurrentSleep   string `json:"current_sleep"`
	CurrentChain   string `json:"current_chain"`
	ImageAvailable bool   `json:"image_available"`
}
type BootIssue struct {
	Level  string `json:"level"` // impossible, broken, untested
	Row    string `json:"row"`
	Reason string `json:"reason"`
}

func BootRules() []BootRule {
	w := BootWorksWith{Chains: []string{"standard"}, KernelLines: []string{"4.19", "5.10"}, Storage: []string{"sd"}}
	return []BootRule{
		{Kind: "bootloader", ID: "standard", Label: "Standard U-Boot, newest test build", WorksWith: w, TestedOn: []string{"R36 Ultra"}},
		{Kind: "bootloader", ID: "new", Label: "New U-Boot, newest test build", WorksWith: w, TestedOn: []string{"R36 Ultra"}},
		{Kind: "bootloader", ID: "file", Label: "File", WorksWith: w},
		{Kind: "chain", ID: "mainline", Label: "Fully mainline", WorksWith: BootWorksWith{Chains: []string{"mainline"}, KernelLines: []string{"4.19", "5.10"}, Storage: []string{"sd"}}, TestedOn: []string{"R36 Ultra"}},
	}
}
func (c BootloaderChoice) ChainID() string {
	if c.Chain == "" {
		return "standard"
	}
	return c.Chain
}
func (c BootloaderChoice) SleepID() string {
	if c.Sleep == "" {
		return "image"
	}
	return c.Sleep
}
func (c BootloaderChoice) LoaderID() string {
	if c.Choice == "" {
		return "image"
	}
	return c.Choice
}
func (c BootloaderChoice) Changed() bool {
	return c.ChainID() != "standard" || c.SleepID() != "image" || c.LoaderID() != "image"
}
func containsBoot(a []string, b string) bool {
	for _, v := range a {
		if v == b {
			return true
		}
	}
	return false
}
func bootAllowed(a []string, b string) bool { return len(a) == 0 || b == "" || containsBoot(a, b) }
func EvaluateBootOptions(c BootloaderChoice, t BootTarget, rules []BootRule) []BootIssue {
	out := []BootIssue{}
	add := func(level, row, reason string) { out = append(out, BootIssue{level, row, reason}) }
	if t.Storage != "" && t.Storage != "sd" {
		add("impossible", "chain", BootloaderOnlySD)
		if c.LoaderID() != "image" {
			add("impossible", "bootloader", BootloaderOnlySD)
		}
		if c.SleepID() != "image" {
			add("impossible", "sleep", BootloaderOnlySD)
		}
		return out
	}
	if c.ChainID() != "standard" && c.ChainID() != "mainline" {
		add("impossible", "chain", "Unknown boot chain.")
		return out
	}
	if c.ChainID() == "standard" && c.SleepID() == "image" && t.CurrentSleep != "" && t.CurrentSleep != "image" && !t.ImageAvailable {
		add("impossible", "sleep", BootloaderNeedImage)
	}
	if t.CurrentChain == "mainline" && c.ChainID() == "standard" && !t.ImageAvailable {
		add("impossible", "chain", BootloaderNeedImage)
	}
	selected := map[string]string{"chain": c.ChainID(), "bootloader": c.LoaderID(), "sleep": c.SleepID()}
	for _, kind := range []string{"bootloader", "sleep", "chain"} {
		id := selected[kind]
		if id == "image" || kind == "chain" && id == "standard" {
			continue
		}
		found := false
		for _, r := range rules {
			if r.Kind != kind || r.ID != id {
				continue
			}
			found = true
			w := r.WorksWith
			for _, p := range []struct {
				values       []string
				value, other string
			}{
				{w.Chains, c.ChainID(), c.ChainID()}, {w.Bootloaders, c.LoaderID(), c.LoaderID()}, {w.KernelLines, t.KernelLine, t.KernelLine}, {w.Profiles, t.Profile, t.Profile}, {w.Storage, t.Storage, t.Storage},
			} {
				if !bootAllowed(p.values, p.value) {
					reason := fmt.Sprintf("Doesn't work with %s.", p.other)
					if p.other == "mainline" && kind != "chain" {
						reason = BootOptionsEmbedded
					}
					add("impossible", kind, reason)
				}
			}
			for _, b := range r.Broken {
				value := map[string]string{"chain": c.ChainID(), "bootloader": c.LoaderID(), "sleep": c.SleepID(), "kernel_line": t.KernelLine, "profile": t.Profile, "storage": t.Storage}[b.Field]
				if value == b.Value {
					add("broken", kind, b.Reason)
				}
			}
			if !containsBoot(r.TestedOn, t.Console) {
				add("untested", kind, BootOptionsUntestedConsole)
			}
			if kind == "sleep" && !containsBoot(r.TestedBootloaders, c.LoaderID()) {
				add("untested", kind, BootOptionsUntestedLoader)
			}
		}
		if !found {
			add("impossible", kind, "This boot option is not available.")
		}
	}
	return out
}

// Only impossible later combinations reset. Broken pairs remain selectable with
// the explicit confirmation required by the owner. The changed row wins.
func ResetBootOptions(c BootloaderChoice, changed string, t BootTarget, rules []BootRule) (BootloaderChoice, []BootIssue) {
	notes := []BootIssue{}
	for n := 0; n < 3; n++ {
		issues := EvaluateBootOptions(c, t, rules)
		reset := false
		seen := map[string]bool{}
		for _, i := range issues {
			if i.Level != "impossible" || i.Row == changed || seen[i.Row] {
				continue
			}
			seen[i.Row] = true
			other := map[string]string{"chain": c.ChainID(), "bootloader": c.LoaderID(), "sleep": c.SleepID()}[changed]
			if other == "" {
				if strings.HasPrefix(i.Reason, "Doesn't work with ") {
					other = strings.TrimSuffix(strings.TrimPrefix(i.Reason, "Doesn't work with "), ".")
				} else if t.Storage != "" && t.Storage != "sd" {
					other = "eMMC"
				} else {
					other = "available boot options"
				}
			}
			switch i.Row {
			case "bootloader":
				if c.LoaderID() == "image" {
					continue
				}
				c.Choice = "image"
				c.Build = nil
			case "sleep":
				if c.SleepID() == "image" {
					continue
				}
				c.Sleep = "image"
			case "chain":
				if c.ChainID() == "standard" {
					continue
				}
				c.Chain = "standard"
			default:
				continue
			}
			notes = append(notes, BootIssue{"reset", i.Row, fmt.Sprintf("Reset: doesn't work with %s", other)})
			reset = true
		}
		if !reset {
			break
		}
	}
	c.Force = false
	return c, notes
}
func CheckBootOptions(c BootloaderChoice, t BootTarget, rules []BootRule) ([]BootIssue, error) {
	issues := EvaluateBootOptions(c, t, rules)
	for _, i := range issues {
		if i.Level == "impossible" || i.Level == "broken" && !c.Force {
			return issues, errors.New(i.Reason)
		}
	}
	return issues, nil
}
func BootOptionsSummary(c BootloaderChoice, issues []BootIssue) string {
	if !c.Changed() {
		return ""
	}
	parts := []string{}
	if c.LoaderID() != "image" {
		parts = append(parts, "Bootloader: "+c.LoaderID())
	}
	if c.SleepID() != "image" {
		parts = append(parts, "Sleep firmware: "+c.SleepID())
	}
	if c.ChainID() != "standard" {
		parts = append(parts, "Boot chain: Fully mainline")
	}
	out := "Advanced boot options changed: " + strings.Join(parts, ", ")
	for _, i := range issues {
		if i.Level == "untested" {
			out += " (untested combination)"
			break
		}
	}
	return out
}
func ImageBootTarget(im *Image, profile string) BootTarget {
	t := BootTarget{Storage: "sd", ImageAvailable: im != nil, Profile: profile}
	if im != nil {
		t.KernelLine = im.Facts["kernel_line"]
		if t.KernelLine == "" {
			t.KernelLine = "4.19"
		}
		for _, p := range im.Profiles {
			if p.ID == profile {
				t.Console = p.Console
			}
		}
	}
	return t
}
