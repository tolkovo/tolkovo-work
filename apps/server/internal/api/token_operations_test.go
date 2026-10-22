package api

import (
	"bufio"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// tokenOperations is the contract's list: an operation with accessToken in
// its security is open to personal tokens, and no other one is. Without
// this a new operation could promise tokens in api/openapi.yaml and answer
// them 403, or open to tokens without the contract saying so.
func TestTokenOperationsMatchContract(t *testing.T) {
	f, err := os.Open("../../../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("open contract: %v", err)
	}
	defer f.Close()
	pathRe := regexp.MustCompile(`^  (/\S+):$`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete):$`)
	var path, method string
	inPaths := false
	contract := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "paths:":
			inPaths = true
		case inPaths && line != "" && !strings.HasPrefix(line, " "):
			inPaths = false // the next top-level key
		case !inPaths:
		case pathRe.MatchString(line):
			path, method = pathRe.FindStringSubmatch(line)[1], ""
		case methodRe.MatchString(line):
			method = strings.ToUpper(methodRe.FindStringSubmatch(line)[1])
		case method != "" && strings.TrimSpace(line) == "- accessToken: []":
			contract[method+" /api/v1"+path] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read contract: %v", err)
	}
	if len(contract) < 5 {
		t.Fatalf("found only %d token operations in the contract: is the parser right?", len(contract))
	}
	for op := range contract {
		if !tokenOperations[op] {
			t.Errorf("%s lists accessToken in the contract but tokenOperations refuses tokens", op)
		}
	}
	for op := range tokenOperations {
		if !contract[op] {
			t.Errorf("%s is open to tokens but the contract does not list accessToken", op)
		}
	}
	if t.Failed() {
		ops := make([]string, 0, len(contract))
		for op := range contract {
			ops = append(ops, op)
		}
		slices.Sort(ops)
		t.Logf("the contract's token operations: %s", strings.Join(ops, ", "))
	}
}
