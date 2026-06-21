package config

import "testing"

func TestSetBudget_RoundTrips(t *testing.T) {
	home := t.TempDir()
	if err := SetBudget(home, 500_000_000); err != nil {
		t.Fatal(err)
	}
	micros, ok, err := LoadBudget(home)
	if err != nil || !ok || micros != 500_000_000 {
		t.Fatalf("LoadBudget after SetBudget = %d,%v,%v; want 500000000,true,nil", micros, ok, err)
	}
	// Overwrite in place; a fractional dollar amount survives the round-trip.
	if err := SetBudget(home, 250_500_000); err != nil {
		t.Fatal(err)
	}
	if micros, _, _ := LoadBudget(home); micros != 250_500_000 {
		t.Errorf("overwrite = %d, want 250500000", micros)
	}
}
