package wsdashboard

import (
	"testing"
	"time"
)

func TestBoardSnapDeck0512Contracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/board-snap-deck-0512.test.cjs")
}

func TestBoardSnapDeck0512Browser(t *testing.T) {
	runSnapDeckBrowserSuite(t, "testdata/board-snap-deck-0512.browser.cjs", 120*time.Second)
}
