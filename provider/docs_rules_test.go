package filescom

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// docRuleName answers the function name of a rule's Edit, which is how a reader finds the rule.
func docRuleName(edit any) string {
	name := runtime.FuncForPC(reflect.ValueOf(edit).Pointer()).Name()
	return name[strings.LastIndex(name, ".")+1:]
}

// Each rule matches exact upstream text and returns the page unchanged on a miss, so an upstream
// rewrite leaves it inert with no error. The bridge matches Path against the page file name with
// filepath.Match (pkg/tfgen/edit_rules.go, editRules.apply).
func TestEveryDocRuleChangesAnUpstreamPage(t *testing.T) {
	rules := Provider().DocRules
	if rules == nil || rules.EditRules == nil {
		t.Fatal("the provider declares no doc edit rules")
	}
	own := rules.EditRules(nil)
	if len(own) == 0 {
		t.Fatal("the provider adds no doc edit rule")
	}

	var pages []string
	for _, dir := range []string{"resources", "data-sources"} {
		found, err := filepath.Glob(filepath.Join("..", "upstream", "docs", dir, "*.md"))
		if err != nil {
			t.Fatalf("glob upstream/docs/%s: %v", dir, err)
		}
		pages = append(pages, found...)
	}
	if len(pages) == 0 {
		t.Fatal("upstream/docs holds no resource or data source page")
	}

	for _, rule := range own {
		name := docRuleName(rule.Edit)
		changed := 0
		for _, page := range pages {
			match, err := filepath.Match(rule.Path, filepath.Base(page))
			if err != nil {
				t.Fatalf("%s: path %q: %v", name, rule.Path, err)
			}
			if !match {
				continue
			}
			content, err := os.ReadFile(page)
			if err != nil {
				t.Fatalf("read %s: %v", page, err)
			}
			edited, err := rule.Edit(filepath.Base(page), content)
			if err != nil {
				t.Fatalf("%s on %s: %v", name, page, err)
			}
			if !bytes.Equal(edited, content) {
				changed++
			}
		}
		if changed == 0 {
			t.Errorf("%s changes no upstream page that matches %q; its upstream target text is gone",
				name, rule.Path)
		}
	}
}
