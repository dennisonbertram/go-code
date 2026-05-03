package main

// SumSlice returns the sum of elements in nums.
func SumSlice(nums []int) int {
	total := 0
	for i := 0; i < len(nums); i++ { // FIXED: use < instead of <=
		total += nums[i]
	}
	return total
}
