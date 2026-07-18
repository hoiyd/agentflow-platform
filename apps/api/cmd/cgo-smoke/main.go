//go:build cgo

package main

/*
#include <stdint.h>

static int32_t add_i32(int32_t a, int32_t b) {
	return a + b;
}

static int32_t context_budget_i32(int32_t window, int32_t output_reserve, int32_t safety_margin) {
	int32_t budget = window - output_reserve - safety_margin;
	if (budget < 0) {
		return 0;
	}
	return budget;
}
*/
import "C"

import "fmt"

func main() {
	sum := C.add_i32(21, 21)
	budget := C.context_budget_i32(4096, 512, 128)

	fmt.Printf("cgo add_i32(21, 21) = %d\n", int32(sum))
	fmt.Printf("cgo context_budget_i32(4096, 512, 128) = %d\n", int32(budget))
}
