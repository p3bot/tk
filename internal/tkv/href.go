package tkv

import "github.com/p3bot/tk/internal/id"

func inspectHref(fullID string) string {
	scope := id.ScopeOfFullID(fullID)
	if !id.IsScopeName(scope) {
		return ""
	}
	return "/scope/" + scope + "/" + fullID
}

func notesListHref(scope string) string {
	return "/scope/" + scope + "/notes"
}

func noteHref(scope, slug string) string {
	return "/scope/" + scope + "/notes/" + slug
}
