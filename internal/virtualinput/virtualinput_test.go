package virtualinput_test

import (
	"encoding/json"
	"testing"
	"time"

	"fastiot/internal/virtualinput"
)

func number(v float64) *float64 { return &v }
func capacity(value string) virtualinput.Definition {
	return virtualinput.Definition{Kind: "number", Unit: "kWh", Purpose: "容量上限", Min: number(0), Max: number(200), Validity: "constant", Value: json.RawMessage(value)}
}
func context(id string, revision uint64) virtualinput.Context {
	return virtualinput.Context{Actor: "engineer", OperationID: id, ExpectedRevision: revision, Now: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)}
}

func timedInput(open bool) virtualinput.Definition {
	return virtualinput.Definition{Kind: "number", Unit: "kW", Purpose: "人工需求", Min: number(-10), Max: number(10), Validity: "timed", TTLSeconds: 60, RuntimeEditable: open}
}

func TestTimedDefinitionMustBeAppliedWithoutInventingValue(t *testing.T) {
	for _, source := range []string{"manual", "simulation"} {
		d, err := virtualinput.New(source, timedInput(false), context("create", 0))
		if err != nil {
			t.Fatalf("create %s: %v", source, err)
		}
		d, err = d.Apply(context("apply", d.Revision))
		if err != nil {
			t.Fatal(err)
		}
		if v := d.Evaluate(context("read", 0)); v.Value != nil || v.Quality == "good" || v.Source != source {
			t.Fatalf("application fabricated a runtime value: %+v", v)
		}
	}
	for _, def := range []virtualinput.Definition{
		{Kind: "number", Unit: "kW", Purpose: "demand", Min: number(-10), Max: number(10), Validity: "constant"},
		{Kind: "number", Unit: "kW", Purpose: "demand", Min: number(-10), Max: number(10), Validity: "timed", TTLSeconds: 0},
	} {
		if _, err := virtualinput.New("manual", def, context("create", 0)); err == nil {
			t.Fatal("unbounded manual input accepted")
		}
	}
}

func TestTimedInputValidityRetryAndDefinitionChanges(t *testing.T) {
	c := context("create", 0)
	c.Epoch = "process-a"
	d, err := virtualinput.New("simulation", timedInput(true), c)
	if err != nil {
		t.Fatal(err)
	}
	c.OperationID, c.ExpectedRevision = "early", d.Revision
	if _, err := d.SetValue(json.RawMessage("5"), c); err == nil {
		t.Fatal("write before apply")
	}
	c.OperationID = "apply"
	d, err = d.Apply(c)
	if err != nil {
		t.Fatal(err)
	}
	c.OperationID, c.ExpectedRevision = "write", d.Revision
	for _, raw := range []string{"11", `"5"`, "null", "false"} {
		if _, err := d.SetValue(json.RawMessage(raw), c); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	d, err = d.SetValue(json.RawMessage("5"), c)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Evaluate(c); string(got.Value) != "5" || got.Source != "simulation" || got.Quality != "good" {
		t.Fatalf("valid input: %+v", got)
	}
	original := d.Current.EnteredAt
	c.Now, c.Monotonic = c.Now.Add(30*time.Second), 30*time.Second
	retry, err := d.SetValue(json.RawMessage("5"), c)
	if err != nil || retry.Revision != d.Revision || retry.Current.EnteredAt != original {
		t.Fatal("retry renewed input")
	}
	for _, change := range []string{"expiry", "monotonic", "rollback", "restart"} {
		read := c
		switch change {
		case "expiry":
			read.Now = original.Add(time.Minute)
		case "monotonic":
			read.Monotonic = time.Minute
		case "rollback":
			read.Now = original.Add(-time.Second)
		case "restart":
			read.Epoch = "process-b"
		}
		got := d.Evaluate(read)
		if got.Quality == "good" || got.Value != nil || got.Retained == nil || got.Retained.EnteredAt != original {
			t.Fatalf("%s resurrected or lost evidence: %+v", change, got)
		}
	}
	changed := timedInput(true)
	changed.Unit = "MW"
	c.OperationID, c.ExpectedRevision = "draft", d.Revision
	d, err = d.SaveDraft(changed, c)
	if err != nil {
		t.Fatal(err)
	}
	if string(d.Evaluate(c).Value) != "5" {
		t.Fatal("draft invalidated active input")
	}
	c.OperationID, c.ExpectedRevision = "apply-new-unit", d.Revision
	d, err = d.Apply(c)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Evaluate(c); got.Value != nil || got.Retained == nil || got.Retained.Definition.Unit != "kW" {
		t.Fatal("new unit reinterpreted old value")
	}
	c.OperationID, c.ExpectedRevision = "restore-draft", d.Revision
	d, _ = d.SaveDraft(timedInput(true), c)
	c.OperationID, c.ExpectedRevision = "restore-apply", d.Revision
	d, _ = d.Apply(c)
	if d.Evaluate(c).Value != nil {
		t.Fatal("restoring old definition resurrected previous input")
	}
	closed, _ := virtualinput.New("manual", timedInput(false), context("closed", 0))
	closed, _ = closed.Apply(context("apply", closed.Revision))
	if _, err := closed.SetValue(json.RawMessage("5"), context("write", closed.Revision)); err == nil {
		t.Fatal("closed input accepted write")
	}
}

func TestTypedConfigurationRejectsMissingAndInvalidValues(t *testing.T) {
	for _, raw := range []string{"", "null", "false", `"100"`, "201", "-1"} {
		t.Run("reject_"+raw, func(t *testing.T) {
			if _, err := virtualinput.New("configuration", capacity(raw), context("create", 0)); err == nil {
				t.Fatalf("accepted invalid numeric configuration %q", raw)
			}
		})
	}
	for _, field := range []string{"unit", "purpose", "range", "validity", "source"} {
		d := capacity("100")
		source := "configuration"
		switch field {
		case "unit":
			d.Unit = ""
		case "purpose":
			d.Purpose = ""
		case "range":
			d.Min = number(300)
		case "validity":
			d.Validity = ""
		case "source":
			source = "measured"
		}
		if _, err := virtualinput.New(source, d, context("create", 0)); err == nil {
			t.Errorf("accepted invalid %s", field)
		}
	}
	for _, item := range []struct {
		name string
		def  virtualinput.Definition
		want string
	}{
		{"zero", capacity("0"), "0"},
		{"false", virtualinput.Definition{Kind: "bool", Unit: "1", Purpose: "允许服务", Validity: "constant", Value: json.RawMessage("false")}, "false"},
		{"enum", virtualinput.Definition{Kind: "enum", Unit: "1", Purpose: "运行选项", Validity: "constant", Choices: []string{"eco", "normal"}, Value: json.RawMessage(`"eco"`)}, `"eco"`},
	} {
		t.Run(item.name, func(t *testing.T) {
			d, err := virtualinput.New("configuration", item.def, context("create", 0))
			if err != nil {
				t.Fatal(err)
			}
			d, err = d.Apply(context("apply", d.Revision))
			if err != nil {
				t.Fatal(err)
			}
			if v := d.Evaluate(context("read", 0)); string(v.Value) != item.want || v.Quality != "good" {
				t.Fatalf("lost explicit typed value: %+v", v)
			}
		})
	}
	badEnum := virtualinput.Definition{Kind: "enum", Unit: "1", Purpose: "选项", Validity: "constant", Choices: []string{"eco"}, Value: json.RawMessage(`"other"`)}
	if _, err := virtualinput.New("configuration", badEnum, context("create", 0)); err == nil {
		t.Fatal("accepted undeclared enum")
	}
}

func TestConfigurationRejectsStaleEditsAndDoesNotReapply(t *testing.T) {
	d, err := virtualinput.New("configuration", capacity("100"), context("create", 0))
	if err != nil {
		t.Fatal(err)
	}
	op := context("apply", d.Revision)
	d, err = d.Apply(op)
	if err != nil {
		t.Fatal(err)
	}
	revision, entered := d.Revision, d.Current.EnteredAt
	for _, edit := range []struct {
		def virtualinput.Definition
		ctx virtualinput.Context
	}{
		{capacity("120"), context("stale", 0)},
		{capacity("999"), context("invalid", revision)},
	} {
		if _, err := d.SaveDraft(edit.def, edit.ctx); err == nil {
			t.Error("invalid/stale draft accepted")
		}
	}
	op.Now = op.Now.Add(time.Hour)
	retry, err := d.Apply(op)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Revision != revision || !retry.Current.EnteredAt.Equal(entered) {
		t.Fatal("operation retry re-applied configuration")
	}
	noop := context("unchanged-apply", revision)
	noop.Now = noop.Now.Add(2 * time.Hour)
	again, err := d.Apply(noop)
	if err != nil {
		t.Fatal(err)
	}
	if again.Revision != revision || !again.Current.EnteredAt.Equal(entered) {
		t.Fatal("unchanged application renewed state")
	}
	badRetry := op
	badRetry.Actor = "someone-else"
	if _, err := d.Apply(badRetry); err == nil {
		t.Fatal("operation ID reused by another actor")
	}
}

func TestConfigurationDraftDoesNotChangeEffectiveValue(t *testing.T) {
	d, err := virtualinput.New("configuration", capacity("100"), context("create", 0))
	if err != nil {
		t.Fatal(err)
	}
	if view := d.Evaluate(context("read", 0)); view.Value != nil || view.Quality == "good" {
		t.Fatalf("creation invented an effective value: %+v", view)
	}
	d, err = d.SaveDraft(capacity("120"), context("draft", d.Revision))
	if err != nil {
		t.Fatal(err)
	}
	if d.Evaluate(context("read", 0)).Value != nil {
		t.Fatal("saving draft applied its value")
	}
	d, err = d.Apply(context("apply", d.Revision))
	if err != nil {
		t.Fatal(err)
	}
	v := d.Evaluate(context("read", 0))
	if string(v.Value) != "120" || v.Quality != "good" || v.Source != "configuration" || d.Current.Actor != "engineer" {
		t.Fatalf("explicit application lost value or provenance: %+v", v)
	}
	d, err = d.SaveDraft(capacity("150"), context("later-draft", d.Revision))
	if err != nil {
		t.Fatal(err)
	}
	if string(d.Evaluate(context("read", 0)).Value) != "120" {
		t.Fatal("later draft replaced the applied value")
	}
}
