package webgrab

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func docFromHTML(t *testing.T, html string) *goquery.Document {
	t.Helper()

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	return doc
}

func TestGrabRejectsInvalidDestinations(t *testing.T) {
	type page struct {
		Title string `grab:"title"`
	}

	var nilPage *page

	tests := []struct {
		name string
		data any
		want string
	}{
		{
			name: "nil interface",
			data: nil,
			want: "non-nil pointer to a struct",
		},
		{
			name: "non pointer",
			data: page{},
			want: "non-nil pointer to a struct",
		},
		{
			name: "nil pointer",
			data: nilPage,
			want: "non-nil pointer to a struct",
		},
		{
			name: "pointer to non struct",
			data: new(string),
			want: "pointer to a struct",
		},
	}

	grabber := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := grabber.Grab("://bad-url", tt.data)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func TestScrapeStructReturnsErrorForUnexportedTaggedField(t *testing.T) {
	type page struct {
		title string `grab:"title"`
	}

	doc := docFromHTML(t, `<html><head><title>Hello</title></head></html>`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "not settable") {
		t.Fatalf("expected settable error, got %q", err.Error())
	}
}

func TestScrapeStructReportsSliceItemErrorsWithoutDroppingSuccessesSilently(t *testing.T) {
	type item struct {
		Title string `grab:".title"`
	}

	type page struct {
		Items []item `context:".item"`
	}

	doc := docFromHTML(t, `
		<div class="item"><span class="title">One</span></div>
		<div class="item"><span class="other">Missing title</span></div>
	`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `Items[1]`) {
		t.Fatalf("expected item index in error, got %q", err.Error())
	}
	if len(data.Items) != 1 {
		t.Fatalf("expected one successful item, got %d", len(data.Items))
	}
	if data.Items[0].Title != "One" {
		t.Fatalf("expected first item title to be %q, got %q", "One", data.Items[0].Title)
	}
}

func TestScrapeStructReturnsExtractErrorsInsteadOfPlaceholderText(t *testing.T) {
	type page struct {
		Title string `grab:"title" extract:"(.+) - Wikipedia"`
	}

	doc := docFromHTML(t, `<html><head><title>Example Domain</title></head></html>`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("expected extract mismatch error, got %q", err.Error())
	}
	if data.Title != "" {
		t.Fatalf("expected title to remain empty, got %q", data.Title)
	}
}

func TestScrapeStructReturnsInvalidExtractRegexErrors(t *testing.T) {
	type page struct {
		Title string `grab:"title" extract:"("`
	}

	doc := docFromHTML(t, `<html><head><title>Example Domain</title></head></html>`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "invalid extract regex") {
		t.Fatalf("expected invalid regex error, got %q", err.Error())
	}
	if data.Title != "" {
		t.Fatalf("expected title to remain empty, got %q", data.Title)
	}
}
