package main

// Primes returns a slice of all primes <= n using an optimized Sieve of Eratosthenes with a bitset.
// n must be >= 2.
func Primes(n int) []int {
	if n < 2 {
		return nil
	}
	if n == 2 {
		return []int{2}
	}
	// Only odd numbers >=3 will be tracked. number represented by index i: 2*i+3
	// Length: (n-1)/2 (since 2 is always prime, start from 3)
	size := (n-1)/2
	// 1 bit for each odd number >= 3 up to n
	bitset := make([]uint64, (size+63)/64) // each uint64 covers 64 odds

	// Sieve: for p in 3,5,...,sqrt(n): mark multiples, skip even multiples
	limit := intSqrt(n)
	for i := 0; 2*i+3 <= limit; i++ {
		if (bitset[i/64]>>(i%64))&1 != 0 {
			continue // not prime
		}
		p := 2*i + 3
		// Mark all multiples of p^2, p^2+p*2, ... up to n
		// index of p^2: k_start = (p*p-3)//2
		for k := (p*p-3)/2; k < size; k += p {
			bitset[k/64] |= 1 << (k % 64)
		}
	}

	primes := make([]int, 1, size/10)
	primes[0] = 2 // 2 is prime
	for i := 0; i < size; i++ {
		if (bitset[i/64]>>(i%64))&1 == 0 {
			primes = append(primes, 2*i+3)
		}
	}
	return primes
}

// intSqrt returns floor(sqrt(n)) for n >= 0
func intSqrt(n int) int {
	if n <= 0 {
		return 0
	}
	left, right := 1, n
	for left <= right {
		mid := (left + right) / 2
		if mid*mid > n {
			right = mid - 1
		} else {
			left = mid + 1
		}
	}
	return left - 1
}
