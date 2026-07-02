package market

import "finador/internal/domain"

func mustDate(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}
