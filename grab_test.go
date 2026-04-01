package webgrab

import (
	"reflect"
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

func TestScrapeStructSkipsInvalidSliceItemsByDefault(t *testing.T) {
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
	if err != nil {
		t.Fatalf("expected no error, got %q", err.Error())
	}
	if len(data.Items) != 1 {
		t.Fatalf("expected one successful item, got %d", len(data.Items))
	}
	if data.Items[0].Title != "One" {
		t.Fatalf("expected first item title to be %q, got %q", "One", data.Items[0].Title)
	}
}

func TestScrapeStructReportsSliceItemErrorsInStrictMode(t *testing.T) {
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

	grabber := New()
	grabber.StrictMode = true

	err := grabber.scrapeStruct(doc, &data)
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

func TestScrapeStructHandlesListMarkupWithOmittedClosingTags(t *testing.T) {
	type page struct {
		Items []string `grab:"li"`
	}

	doc := docFromHTML(t, `<ul><li>One<li>Two<li>Three</ul>`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err != nil {
		t.Fatalf("expected no error, got %q", err.Error())
	}

	want := []string{"One", "Two", "Three"}
	if !reflect.DeepEqual(data.Items, want) {
		t.Fatalf("expected items %v, got %v", want, data.Items)
	}
}

func TestScrapeStructHandlesTableMarkupWithMissingRowAndCellClosures(t *testing.T) {
	type row struct {
		Title string `grab:".title"`
		Price string `grab:".price"`
	}

	type page struct {
		Rows []row `context:"tr"`
	}

	doc := docFromHTML(t, `
		<table>
			<tr><td class=title>One<td class=price>$10
			<tr><td class=title>Two<td class=price>$20
		</table>
	`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err != nil {
		t.Fatalf("expected no error, got %q", err.Error())
	}

	want := []row{
		{Title: "One", Price: "$10"},
		{Title: "Two", Price: "$20"},
	}
	if !reflect.DeepEqual(data.Rows, want) {
		t.Fatalf("expected rows %#v, got %#v", want, data.Rows)
	}
}

func TestScrapeStructHandlesHTMLFragmentsWithoutDocumentWrappers(t *testing.T) {
	type page struct {
		Headline string   `grab:".headline"`
		Links    []string `grab:"a[href]" attr:"href"`
	}

	doc := docFromHTML(t, `<div class=headline>Breaking News</div><a href=/one>One</a><a href=/two>Two</a>`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err != nil {
		t.Fatalf("expected no error, got %q", err.Error())
	}

	if data.Headline != "Breaking News" {
		t.Fatalf("expected headline %q, got %q", "Breaking News", data.Headline)
	}

	want := []string{"/one", "/two"}
	if !reflect.DeepEqual(data.Links, want) {
		t.Fatalf("expected links %v, got %v", want, data.Links)
	}
}

func TestScrapeStructHandlesUppercaseTagsAndUnquotedAttributes(t *testing.T) {
	type page struct {
		Headline string   `grab:"h1"`
		Links    []string `grab:"a[href]" attr:"href"`
	}

	doc := docFromHTML(t, `<HTML><BODY><H1>News</H1><A HREF=/one>One</A><A HREF=/two>Two</A></BODY></HTML>`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err != nil {
		t.Fatalf("expected no error, got %q", err.Error())
	}

	if data.Headline != "News" {
		t.Fatalf("expected headline %q, got %q", "News", data.Headline)
	}

	want := []string{"/one", "/two"}
	if !reflect.DeepEqual(data.Links, want) {
		t.Fatalf("expected links %v, got %v", want, data.Links)
	}
}

func TestScrapeStructHandlesNestedContextWithOptionalParagraphClosures(t *testing.T) {
	type profile struct {
		Name  string `grab:".name"`
		Email string `grab:".email"`
	}

	type page struct {
		Profile profile `context:".profile"`
	}

	doc := docFromHTML(t, `<div class=profile><p class=name>Alice<p class=email>alice@example.com</div>`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err != nil {
		t.Fatalf("expected no error, got %q", err.Error())
	}

	want := profile{Name: "Alice", Email: "alice@example.com"}
	if !reflect.DeepEqual(data.Profile, want) {
		t.Fatalf("expected profile %#v, got %#v", want, data.Profile)
	}
}

func TestScrapeStructHandlesAttributeExtractionFromMalformedLinks(t *testing.T) {
	type page struct {
		StoryIDs []string `grab:"a[href]" attr:"href" extract:"id=(\\d+)"`
	}

	doc := docFromHTML(t, `<div><a href=/story?id=42>First<a href=/story?id=314>Second</div>`)
	var data page

	err := New().scrapeStruct(doc, &data)
	if err != nil {
		t.Fatalf("expected no error, got %q", err.Error())
	}

	want := []string{"42", "314"}
	if !reflect.DeepEqual(data.StoryIDs, want) {
		t.Fatalf("expected story ids %v, got %v", want, data.StoryIDs)
	}
}
