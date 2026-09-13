package tkv

import "github.com/p3bot/tk/internal/id"

func inspectHref(fullID string) string {
	scope := id.ScopeOfFullID(fullID)
	if !id.IsScopeName(scope) {
		return ""
	}
	return "/scope/" + scope + "/" + fullID
}

func inspectEditHref(fullID string) string {
	scope := id.ScopeOfFullID(fullID)
	if !id.IsScopeName(scope) {
		return ""
	}
	return "/scope/" + scope + "/edit/" + fullID
}

func notesPickHref() string {
	return "/notes"
}

func notesListHref(scope string) string {
	return "/scope/" + scope + "/notes"
}

func noteHref(scope, slug string) string {
	return "/scope/" + scope + "/notes/" + slug
}

func noteEditHref(scope, slug string) string {
	return noteHref(scope, slug) + "/edit"
}

func noteViewHref(scope, slug, def string) string {
	if slug == def {
		return notesListHref(scope)
	}
	return noteHref(scope, slug)
}
