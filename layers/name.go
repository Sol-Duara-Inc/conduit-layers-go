package layers

import (
	"regexp"
	"strings"
)

const sanctionedNamespace = "dev.cdevents"

var (
	versionSegRe   = regexp.MustCompile(`^[0-9]+$`)
	lowerWordRe    = regexp.MustCompile(`^[a-z]+$`)
	namespaceSegRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

type parsedName struct {
	Namespace string
	Subject   string
	Predicate string
	Version   string
}

// parseTypeName splits <namespace>.<subject>.<predicate>.<version> from the
// right: the last three dot-segments are MAJOR.MINOR.PATCH, then predicate,
// then subject, and everything left (at least one segment) is the namespace.
func parseTypeName(name string) (parsedName, bool) {
	segs := strings.Split(name, ".")
	if len(segs) < 6 {
		return parsedName{}, false
	}
	n := len(segs)
	for _, v := range segs[n-3:] {
		if !versionSegRe.MatchString(v) {
			return parsedName{}, false
		}
	}
	predicate := segs[n-4]
	subject := segs[n-5]
	if !lowerWordRe.MatchString(predicate) || !lowerWordRe.MatchString(subject) {
		return parsedName{}, false
	}
	for _, s := range segs[:n-5] {
		if !namespaceSegRe.MatchString(s) {
			return parsedName{}, false
		}
	}
	return parsedName{
		Namespace: strings.Join(segs[:n-5], "."),
		Subject:   subject,
		Predicate: predicate,
		Version:   strings.Join(segs[n-3:], "."),
	}, true
}

func (p parsedName) sanctioned() bool { return p.Namespace == sanctionedNamespace }
