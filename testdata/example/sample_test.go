package sample

import (
	"fmt"
	"testing"
)

func TestPasses(t *testing.T) {
	fmt.Println("hello from TestPasses")
}

func TestFails(t *testing.T) {
	t.Fatalf("boom: want %d got %d", 1, 2)
}

func TestSkips(t *testing.T) {
	t.Skip("not supported here")
}

func TestWithSubtests(t *testing.T) {
	t.Run("alpha", func(t *testing.T) {})
	t.Run("beta", func(t *testing.T) {
		t.Errorf("beta failed")
	})
}
