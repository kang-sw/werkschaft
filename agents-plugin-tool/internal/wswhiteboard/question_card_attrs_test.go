package wswhiteboard

import (
	"strings"
	"testing"
	"time"
)

func TestQuestionCardDataAttributes(t *testing.T) {
	st := &State{}
	env := Env{Now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	apply := func(cmd string) {
		t.Helper()
		if _, probs := Apply(st, cmd, env); len(probs) != 0 {
			t.Fatalf("Apply(%q) problems: %v", cmd, probs)
		}
	}

	// Open policy item (1), settled policy item (2), settled check done (3),
	// settled check no (4), assumed technical item (5), withdrawn announced item (6).
	apply(`<wb:topic name="Design">
		<wb:add class="policy"><wb:title>Open policy choice</wb:title><wb:option>A</wb:option><wb:option>B</wb:option></wb:add>
		<wb:add class="policy"><wb:title>Answered policy choice</wb:title><wb:option>A</wb:option><wb:option>B</wb:option></wb:add>
		<wb:add class="check"><wb:title>Verified check ok</wb:title></wb:add>
		<wb:add class="check"><wb:title>Rejected check no</wb:title></wb:add>
		<wb:add class="technical"><wb:title>Assumed technical choice</wb:title></wb:add>
		<wb:add class="announced"><wb:title>Withdrawn notice</wb:title></wb:add>
		<wb:add class="check"><wb:title>Assumed check row</wb:title></wb:add>
	</wb:topic>`)
	apply(`<wb:answer id="2" value="B">B</wb:answer>
		<wb:answer id="3" value="ok">ok</wb:answer>
		<wb:answer id="4" value="no">needs retry</wb:answer>
		<wb:answer id="5" value="ok" assumed="true">default ok</wb:answer>
		<wb:patch id="6" status="withdrawn"/>
		<wb:answer id="7" value="ok" assumed="true">assumed check ok</wb:answer>`)

	view := BuildView(st, ViewInput{Layout: LayoutFull, ReplyID: "attrs"})
	view.Lang = "en"
	regions, err := RenderRegions(view)
	if err != nil {
		t.Fatalf("RenderRegions: %v", err)
	}
	html := string(regions.Board)

	// Open item (1) ask-time historyitem carries data-item, data-class, data-state="ask".
	if !strings.Contains(html, `data-item="1" data-class="policy" data-state="ask"`) {
		t.Errorf("expected open ask-time historyitem for item 1 with data-state=\"ask\", got:\n%s", html)
	}
	// Answered policy (2): original-question carries data-item & data-class (no data-state="settled"),
	// and settlement historyitem carries data-item="2" data-class="policy" data-state="settled".
	if !strings.Contains(html, `original-question policy" id="item-2" data-item="2" data-class="policy"`) {
		t.Errorf("expected original-question for item 2 with data-item and data-class")
	}
	if !strings.Contains(html, `data-item="2" data-class="policy" data-state="settled"`) {
		t.Errorf("expected settled historyitem for item 2 with data-state=\"settled\"")
	}
	// Settled checks (3 done, 4 no) carry data-state="settled" on historyitem,
	// and assumed check (7) renders settled ckrow with data-item and data-class.
	if !strings.Contains(html, `data-item="3" data-class="check" data-state="settled"`) {
		t.Errorf("expected settled historyitem for check 3 with data-item and data-class")
	}
	if !strings.Contains(html, `data-item="4" data-class="check" data-state="settled"`) {
		t.Errorf("expected settled historyitem for check 4 with data-item and data-class")
	}
	if !strings.Contains(html, `id="check-7" data-item="7" data-class="check"`) {
		t.Errorf("expected settled ckrow for assumed check 7 with data-item and data-class")
	}
	// Assumed (5) and withdrawn (6) carry data-state="settled".
	if !strings.Contains(html, `data-item="5" data-class="technical" data-state="settled"`) {
		t.Errorf("expected settled historyitem for assumed item 5")
	}
	if !strings.Contains(html, `data-item="6" data-class="announced" data-state="settled"`) {
		t.Errorf("expected settled historyitem for withdrawn item 6")
	}
}
