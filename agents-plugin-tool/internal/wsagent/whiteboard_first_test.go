package wsagent

import (
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsinbox"
)

func TestTurnInputWhiteboardDirectiveRidesTheAmbientBlockOnce(t *testing.T) {
	block := ambientBlock(WorkflowLanguageInstruction("Korean"))
	lead := &conv{rec: Record{Lead: true, Backend: BackendClaude}, ambient: block}

	if got, want := lead.turnInput("hi", false), "hi\n\n"+block; got != want {
		t.Fatalf("unflagged turn changed: %q", got)
	}
	got := lead.turnInput("hi", true)
	if strings.Count(got, whiteboardFirstDirective) != 1 || strings.Count(got, "<"+ambientBlockName+">") != 1 {
		t.Fatalf("flagged turn must carry the directive once inside one ambient block: %q", got)
	}
	if !strings.Contains(got, WorkflowLanguageInstruction("Korean")) || !strings.HasSuffix(got, "</"+ambientBlockName+">") {
		t.Fatalf("directive must join the existing block, not replace it: %q", got)
	}
	if got != lead.turnInput("hi", true) {
		t.Fatal("composing the same flagged turn twice must be identical (retry safety)")
	}

	bare := &conv{rec: Record{Lead: true, Backend: BackendClaude}}
	if got := bare.turnInput("hi", true); got != "hi\n\n"+ambientBlock(whiteboardFirstDirective) {
		t.Fatalf("flagged turn with no ambient = %q", got)
	}

	worker := &conv{rec: Record{Backend: BackendClaude}}
	if got := worker.turnInput("hi", true); got != "hi" {
		t.Fatalf("a non-lead has no ambient and must not gain the directive: %q", got)
	}
}

func TestSteerWithDirectiveUnionsFlaggedRecords(t *testing.T) {
	lead := &conv{rec: Record{Lead: true}}
	plain := wsinbox.Record{}
	flagged := wsinbox.Record{Entry: wsinbox.Entry{WhiteboardFirst: true}}

	if got := lead.steerWithDirective("text", []wsinbox.Record{plain, plain}); got != "text" {
		t.Fatalf("unflagged steer changed: %q", got)
	}
	got := lead.steerWithDirective("text", []wsinbox.Record{plain, flagged, flagged})
	if strings.Count(got, whiteboardFirstDirective) != 1 || !strings.HasPrefix(got, "text\n\n<"+ambientBlockName+">") {
		t.Fatalf("mixed steer must carry the directive once: %q", got)
	}
	worker := &conv{rec: Record{}}
	if got := worker.steerWithDirective("text", []wsinbox.Record{flagged}); got != "text" {
		t.Fatalf("worker steer = %q", got)
	}
}

func TestAmbientWithDirectiveFallsBackToASecondBlock(t *testing.T) {
	got := ambientWithDirective("custom ambient", whiteboardFirstDirective)
	if !strings.HasPrefix(got, "custom ambient\n\n<"+ambientBlockName+">") || strings.Count(got, whiteboardFirstDirective) != 1 {
		t.Fatalf("ambientWithDirective = %q", got)
	}
}

func TestDirectedSteerRequeuesWithoutTheDirective(t *testing.T) {
	lead := &conv{rec: Record{Lead: true}}
	flagged := []wsinbox.Record{{Entry: wsinbox.Entry{WhiteboardFirst: true}}}
	steer := lead.directedSteer("steer text", flagged, 3)
	if !strings.Contains(steer.Text, whiteboardFirstDirective) || steer.Clean != "steer text" {
		t.Fatalf("flagged steer = %+v", steer)
	}
	if plain := lead.directedSteer("steer text", []wsinbox.Record{{}}, 3); plain.Clean != "" || plain.Text != "steer text" {
		t.Fatalf("unflagged steer must stay byte-identical: %+v", plain)
	}
	rec := &Record{DurableSteers: []durableSteer{steer}}
	requeueDurableSteers(rec)
	if len(rec.Reports) != 1 || rec.Reports[0] != "steer text" || len(rec.DurableSteers) != 0 {
		t.Fatalf("requeued reports = %q", rec.Reports)
	}
}
