package app

import (
	"testing"
)

func TestPositionOpposesSignal(t *testing.T) {
	if !positionOpposesSignal(100, -0.5) {
		t.Fatal("long vs sell")
	}
	if !positionOpposesSignal(-50, 1) {
		t.Fatal("short vs buy")
	}
	if positionOpposesSignal(100, 0.5) {
		t.Fatal("long vs buy not opposite")
	}
	if positionOpposesSignal(0, -1) {
		t.Fatal("flat")
	}
	if positionOpposesSignal(10, 0) {
		t.Fatal("zero signal dir")
	}
}
