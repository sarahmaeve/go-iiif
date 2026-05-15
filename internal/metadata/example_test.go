package metadata_test

import (
	"fmt"

	"github.com/sarahmaeve/go-iiif/internal/metadata"
)

func ExampleParseDateRange() {
	dr, _ := metadata.ParseDateRange("circa 1480")
	fmt.Println(dr.Start, dr.End)
	// Output: 1460 1500
}

func ExampleParseDateRange_romanCentury() {
	dr, _ := metadata.ParseDateRange("XVe siècle")
	fmt.Println(dr.Start, dr.End)
	// Output: 1401 1500
}

func ExampleParseLanguage() {
	code, _ := metadata.ParseLanguage("français")
	fmt.Println(code)
	// Output: fr
}

func ExampleBuildWorkRecord() {
	entries := []metadata.MetadataEntry{
		{Label: "Language", Value: "Latin"},
		{Label: "Date", Value: "1501-1550"},
	}
	mapping := metadata.FieldMapping{
		"language": metadata.FieldLanguage,
		"date":     metadata.FieldDate,
	}
	rec := metadata.BuildWorkRecord(entries, mapping)
	fmt.Println(rec.Langs, rec.DateRange.Start, rec.DateRange.End)
	// Output: [la] 1501 1550
}
