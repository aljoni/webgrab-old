package webgrab

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Tag names for struct fields.
const (
	tagNameGrab      = "grab"
	tagNameExtract   = "extract"
	tagNameFilter    = "filter"
	tagNameAttribute = "attr"
	tagNameContext   = "context"
)

// Grabber configures and performs web scraping.
type Grabber struct {
	Timeout      int
	MaxRedirects int
	StrictMode   bool
	UserAgent    string
}

// grabTag represents a single tag for scraping.
type grabTag struct {
	Field     string
	FieldType reflect.Type
	Selector  string
	Attribute string
	Extract   string
	Filter    string
	Context   string
}

func (t grabTag) configured() bool {
	return t.Selector != "" || t.Attribute != "" || t.Extract != "" || t.Filter != "" || t.Context != ""
}

// parseStruct parses struct tags for scraping.
func parseStruct(t reflect.Type) []grabTag {
	var tags []grabTag
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tags = append(tags, grabTag{
			Field:     field.Name,
			FieldType: field.Type,
			Selector:  field.Tag.Get(tagNameGrab),
			Extract:   field.Tag.Get(tagNameExtract),
			Filter:    field.Tag.Get(tagNameFilter),
			Attribute: field.Tag.Get(tagNameAttribute),
			Context:   field.Tag.Get(tagNameContext),
		})
	}
	return tags
}

func validateDestination(data any) (reflect.Value, error) {
	if data == nil {
		return reflect.Value{}, fmt.Errorf("data must be a non-nil pointer to a struct")
	}

	value := reflect.ValueOf(data)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return reflect.Value{}, fmt.Errorf("data must be a non-nil pointer to a struct")
	}

	elem := value.Elem()
	if elem.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("data must be a pointer to a struct")
	}

	return elem, nil
}

// Grab fetches and populates data from a URL into the provided struct pointer.
func (g Grabber) Grab(url string, data any) error {
	if _, err := validateDestination(data); err != nil {
		return err
	}
	client := &http.Client{
		Timeout: time.Second * time.Duration(g.Timeout),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= g.MaxRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", g.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return err
	}
	return g.scrapeStruct(doc, data)
}

// filter checks if a string matches a regex pattern.
func (g Grabber) filter(str, regex string) bool {
	re, err := regexp.Compile(regex)
	if err != nil {
		return false
	}
	return re.MatchString(str)
}

// extract extracts a substring matching a regex pattern.
func (g Grabber) extract(str, regex string) (string, error) {
	re, err := regexp.Compile(regex)
	if err != nil {
		return "", fmt.Errorf("invalid extract regex %q: %w", regex, err)
	}
	match := re.FindStringSubmatch(str)
	if len(match) < 2 {
		return "", fmt.Errorf("extract regex %q did not match", regex)
	}
	return match[1], nil
}

// clean removes unwanted whitespace and extracts substrings from the scraped text.
func (g Grabber) clean(str string, tag grabTag) (string, error) {
	if tag.Extract != "" {
		var err error
		str, err = g.extract(str, tag.Extract)
		if err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(str), nil
}

// scrape performs the actual scraping for a single tag.
func (g Grabber) scrape(doc *goquery.Document, tag grabTag) (string, error) {
	sel := doc.Find(tag.Selector)
	if sel.Length() == 0 {
		return "", fmt.Errorf("tag not found: %s", tag.Selector)
	}
	sel = sel.First()
	val := ""
	if tag.Attribute == "" {
		val = sel.Text()
	} else {
		val = sel.AttrOr(tag.Attribute, "")
	}
	if tag.Filter != "" && !g.filter(val, tag.Filter) {
		return "", fmt.Errorf("tag does not match filter: %s", tag.Filter)
	}
	return g.clean(val, tag)
}

// scrapeSlice performs the actual scraping for a slice of tags.
func (g Grabber) scrapeSlice(doc *goquery.Document, tag grabTag) ([]string, error) {
	var strs []string
	var errs []error
	sel := doc.Find(tag.Selector)
	if sel.Length() == 0 {
		return nil, fmt.Errorf("tag not found: %s", tag.Selector)
	}
	sel.Each(func(i int, s *goquery.Selection) {
		val := ""
		if tag.Attribute == "" {
			val = s.Text()
		} else {
			val = s.AttrOr(tag.Attribute, "")
		}
		if tag.Filter != "" && !g.filter(val, tag.Filter) {
			return
		}
		cleaned, err := g.clean(val, tag)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s[%d]: %w", tag.Selector, i, err))
			return
		}
		strs = append(strs, cleaned)
	})
	if len(errs) > 0 {
		return strs, errors.Join(errs...)
	}
	return strs, nil
}

// scrapeStruct performs the actual scraping for a nested struct.
func (g Grabber) scrapeStruct(doc *goquery.Document, nested any) error {
	val, err := validateDestination(nested)
	if err != nil {
		return err
	}
	tags := parseStruct(val.Type())
	var errs []error
	for _, tag := range tags {
		field := val.FieldByName(tag.Field)
		if !field.CanSet() {
			if tag.configured() {
				errs = append(errs, fmt.Errorf("field %s is not settable; use an exported field", tag.Field))
			}
			continue
		}
		switch tag.FieldType.Kind() {
		case reflect.Struct:
			// If context is set, restrict doc to that selection
			subDoc := doc
			if tag.Context != "" {
				sel := doc.Find(tag.Context)
				if sel.Length() == 0 {
					errs = append(errs, fmt.Errorf("%s: context not found: %s", tag.Field, tag.Context))
					continue
				}
				node := sel.First().Nodes[0]
				subDoc = goquery.NewDocumentFromNode(node)
			}
			if err := g.scrapeStruct(subDoc, field.Addr().Interface()); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", tag.Field, err))
			}
		case reflect.Slice:
			elemType := tag.FieldType.Elem()
			if elemType.Kind() == reflect.Struct {
				// Slices of structs: context tag is required
				contextSelector := tag.Context
				if contextSelector == "" {
					errs = append(errs, fmt.Errorf("slice of structs requires 'context' tag on field %s", tag.Field))
					continue
				}
				sel := doc.Find(contextSelector)
				if sel.Length() == 0 {
					errs = append(errs, fmt.Errorf("%s: context not found: %s", tag.Field, contextSelector))
					continue
				}
				slice := reflect.MakeSlice(tag.FieldType, 0, sel.Length())
				var itemErrs []error
				sel.Each(func(i int, s *goquery.Selection) {
					elemPtr := reflect.New(elemType)
					subDoc := goquery.NewDocumentFromNode(s.Nodes[0])
					if err := g.scrapeStruct(subDoc, elemPtr.Interface()); err != nil {
						if g.StrictMode {
							itemErrs = append(itemErrs, fmt.Errorf("%s[%d] within %q: %w", tag.Field, i, contextSelector, err))
						}
						return
					}
					slice = reflect.Append(slice, elemPtr.Elem())
				})
				field.Set(slice)
				if len(itemErrs) > 0 {
					errs = append(errs, errors.Join(itemErrs...))
				}
			} else if elemType.Kind() == reflect.String {
				if tag.Selector == "" {
					continue
				}
				strings, err := g.scrapeSlice(doc, tag)
				if len(strings) > 0 {
					slice := reflect.MakeSlice(tag.FieldType, len(strings), len(strings))
					for i, str := range strings {
						slice.Index(i).SetString(str)
					}
					field.Set(slice)
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", tag.Field, err))
				}
			}
		case reflect.String:
			if tag.Selector == "" {
				continue
			}
			str, err := g.scrape(doc, tag)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", tag.Field, err))
				continue
			}
			field.SetString(str)
		default:
			if tag.configured() {
				errs = append(errs, fmt.Errorf("field %s has unsupported type %s", tag.Field, tag.FieldType))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// New returns a new Grabber with default values.
func New() *Grabber {
	return &Grabber{
		Timeout:      10,
		MaxRedirects: 10,
		StrictMode:   false,
		UserAgent:    "Mozilla/5.0 (compatible; WebGrab/1.0;) Go",
	}
}
