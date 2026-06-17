package main

import (
	"fmt"
	"time"
)

// DCPState holds all Dynamic Context Pruning state per chat.
type DCPState struct {
	CompressedRanges  []CompressedRange   `json:"compressedRanges"`
	SeenToolCalls     map[string]int      `json:"seenToolCalls"`
	ErrorToolCalls    []ErrorToolEntry    `json:"errorToolCalls"`
	LastNudgeStep     int                 `json:"lastNudgeStep"`
	LastCompressStep  int                 `json:"lastCompressStep"`
	TotalPrunedTokens int                 `json:"totalPrunedTokens"`
	CurrentTurn       int                 `json:"currentTurn"`
	CurrentTokens     int                 `json:"currentTokens"`
	NextRangeID       int                 `json:"nextRangeID"`
}

type CompressedRange struct {
	ID            int       `json:"id"`
	StartIdx      int       `json:"startIdx"`
	EndIdx        int       `json:"endIdx"`
	Summary       string    `json:"summary"`
	SummaryTokens int       `json:"summaryTokens"`
	Topic         string    `json:"topic"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"createdAt"`
}

type ErrorToolEntry struct {
	ToolCallID string `json:"toolCallID"`
	Name       string `json:"name"`
	Args       string `json:"args"`
	MessageIdx int    `json:"messageIdx"`
	Turn       int    `json:"turn"`
}

func NewDCPState() *DCPState {
	return &DCPState{
		SeenToolCalls:  make(map[string]int),
		ErrorToolCalls: []ErrorToolEntry{},
		NextRangeID:    1,
	}
}

func (d *DCPState) IsEmpty() bool {
	return len(d.CompressedRanges) == 0 &&
		len(d.SeenToolCalls) == 0 &&
		len(d.ErrorToolCalls) == 0 &&
		d.TotalPrunedTokens == 0
}

func (d *DCPState) Summary() string {
	compressed := 0
	for _, r := range d.CompressedRanges {
		if r.Active {
			compressed++
		}
	}
	return fmt.Sprintf(
		"DCP: %d compressed blocks, %d deduped, %d pending errors, ~%d tokens saved",
		compressed, len(d.SeenToolCalls), len(d.ErrorToolCalls), d.TotalPrunedTokens,
	)
}
