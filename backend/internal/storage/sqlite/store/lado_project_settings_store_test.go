package store_test

import (
	"context"
	"testing"
	"time"
)

func TestLadoProjectSettingsDefaultToModeOff(t *testing.T) {
	s := newTestStore(t)

	got, err := s.LadoProjectSettings(context.Background(), "mer")
	if err != nil {
		t.Fatalf("LadoProjectSettings: %v", err)
	}
	if got.ProjectID != "mer" || got.LadoMode || !got.UpdatedAt.IsZero() {
		t.Fatalf("settings = %+v, want project mer with LADO mode off and no update time", got)
	}
}

func TestSetLadoModeStoresTheLatestChoice(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	turnedOn := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

	stored, err := s.SetLadoMode(ctx, "mer", true, turnedOn)
	if err != nil {
		t.Fatalf("SetLadoMode on: %v", err)
	}
	if !stored.LadoMode || !stored.UpdatedAt.Equal(turnedOn) {
		t.Fatalf("stored = %+v, want LADO mode on at %s", stored, turnedOn)
	}

	turnedOff := turnedOn.Add(time.Hour)
	if _, err := s.SetLadoMode(ctx, "mer", false, turnedOff); err != nil {
		t.Fatalf("SetLadoMode off: %v", err)
	}
	got, err := s.LadoProjectSettings(ctx, "mer")
	if err != nil {
		t.Fatalf("LadoProjectSettings: %v", err)
	}
	if got.LadoMode || !got.UpdatedAt.Equal(turnedOff) {
		t.Fatalf("settings = %+v, want LADO mode off at %s", got, turnedOff)
	}

	other, err := s.LadoProjectSettings(ctx, "other")
	if err != nil {
		t.Fatalf("LadoProjectSettings other: %v", err)
	}
	if other.LadoMode {
		t.Fatalf("another project's settings = %+v, want LADO mode off", other)
	}
}
