package sample

import "testing"

func TestExtraOne(t *testing.T) {}

func TestExtraTwo(t *testing.T) {
	t.Skip("demonstrates second-file grouping")
}
