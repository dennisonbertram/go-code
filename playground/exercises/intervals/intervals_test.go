package main

import (
	"reflect"
	"testing"
)

func TestMergeIntervals(t *testing.T) {
	tests := []struct {
		name      string
		intervals [][]int
		want      [][]int
	}{
		{
			name:      "empty input",
			intervals: [][]int{},
			want:      [][]int{},
		},
		{
			name:      "single interval",
			intervals: [][]int{{2, 5}},
			want:      [][]int{{2, 5}},
		},
		{
			name:      "no overlaps",
			intervals: [][]int{{1, 2}, {3, 4}, {5, 6}},
			want:      [][]int{{1, 2}, {3, 4}, {5, 6}},
		},
		{
			name:      "all overlapping",
			intervals: [][]int{{1, 4}, {2, 5}, {3, 6}},
			want:      [][]int{{1, 6}},
		},
		{
			name:      "adjacent intervals",
			intervals: [][]int{{1, 2}, {2, 3}, {3, 4}},
			want:      [][]int{{1, 4}},
		},
		{
			name:      "example input",
			intervals: [][]int{{1, 3}, {2, 6}, {8, 10}, {15, 18}},
			want:      [][]int{{1, 6}, {8, 10}, {15, 18}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeIntervals(tt.intervals)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
