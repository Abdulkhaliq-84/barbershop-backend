package shared_test

import (
	"fmt"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Example functions are documentation that the test runner executes: the
// "Output:" comment must match what they print, so docs never go stale.

func ExampleNewPhoneNumber() {
	p, err := shared.NewPhoneNumber("٠٥٥ ١٢٣ ٤٥٦٧") // typed with Arabic-Indic digits
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(p)          // E.164, for storage and SMS
	fmt.Println(p.Masked()) // for screens and logs
	// Output:
	// +966551234567
	// +9665•••••567
}

func ExampleMoney_Add() {
	haircut, beard := shared.Halalas(6000), shared.Halalas(3500)
	total, err := haircut.Add(beard)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(total)
	// Output: 95.00 SAR
}

func ExampleLocalizedText_In() {
	name, _ := shared.NewLocalizedText("قص شعر", "Haircut")
	fmt.Println(name.In(shared.ParseLanguage("ar-SA")))
	fmt.Println(name.In(shared.ParseLanguage("en-US")))
	// Output:
	// قص شعر
	// Haircut
}
