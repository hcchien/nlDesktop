// Package access 執行三層權限規則（list operation / row filter / field），
// 透過 ent interceptor 與 hook 在資料層統一裁決 —— GraphQL 與 MCP 都無法繞過。
//
// 規則內容不在此定義：執行期從 nl DSL registry（lists package 的宣告）導出。
package access

import (
	"sync"

	"github.com/hcchien/nl/nl"
)

// 角色，對照 e-info 的 User.role。
const (
	RoleAdmin       = "admin"
	RoleModerator   = "moderator"
	RoleEditor      = "editor"
	RoleContributor = "contributor"
)

// Op 是 list 層級的操作種類。
type Op string

const (
	OpQuery  Op = "query"
	OpCreate Op = "create"
	OpUpdate Op = "update"
	OpDelete Op = "delete"
)

// RoleSet 是允許的角色集合。
type RoleSet map[string]struct{}

// Roles 建立角色集合。
func Roles(rs ...string) RoleSet {
	s := make(RoleSet, len(rs))
	for _, r := range rs {
		s[r] = struct{}{}
	}
	return s
}

// Has 回報角色是否在集合內。
func (s RoleSet) Has(role string) bool {
	_, ok := s[role]
	return ok
}

// FieldRule 是 field 層級規則；nil 的 RoleSet 表示不額外限制。
type FieldRule struct {
	Read   RoleSet
	Create RoleSet
	Update RoleSet
}

// ListRules 是一個 list 的完整權限規則。
type ListRules struct {
	// Operations：各操作允許的角色（含 owner-scoped 角色）。缺少的操作視為拒絕所有人。
	Operations map[Op]RoleSet
	// OwnerScoped：這些操作下，集合內的角色只能觸及自己建立的資料（row-level filter）。
	OwnerScoped map[Op]RoleSet
	// Fields：field 層級規則，key 為 GraphQL 欄位名（camelCase）。
	Fields map[string]FieldRule
}

var (
	buildOnce sync.Once
	rules     map[string]ListRules
)

// RulesFor 回傳 list 的權限規則（從 nl registry 導出，lazily built）。
func RulesFor(list string) (ListRules, bool) {
	buildOnce.Do(build)
	r, ok := rules[list]
	return r, ok
}

func build() {
	rules = map[string]ListRules{}
	for _, l := range nl.Registry() {
		lr := ListRules{
			Operations:  map[Op]RoleSet{},
			OwnerScoped: map[Op]RoleSet{},
			Fields:      map[string]FieldRule{},
		}
		for op, rule := range map[Op]nl.Rule{
			OpQuery:  l.Ops.Query,
			OpCreate: l.Ops.Create,
			OpUpdate: l.Ops.Update,
			OpDelete: l.Ops.Delete,
		} {
			// 可操作 = 直接放行 ∪ owner-scoped
			lr.Operations[op] = Roles(append(append([]string{}, rule.Allowed...), rule.OwnerScoped...)...)
			if len(rule.OwnerScoped) > 0 {
				lr.OwnerScoped[op] = Roles(rule.OwnerScoped...)
			}
		}
		for fname, fr := range l.FieldAccess {
			nfr := FieldRule{}
			if fr.Read != nil {
				nfr.Read = Roles(fr.Read...)
			}
			if fr.Create != nil {
				nfr.Create = Roles(fr.Create...)
			}
			if fr.Update != nil {
				nfr.Update = Roles(fr.Update...)
			}
			lr.Fields[fname] = nfr
		}
		rules[l.Name] = lr
	}
}

// CanOperate 回報角色是否可對 list 執行操作。未註冊的 list 一律拒絕。
func CanOperate(list string, op Op, role string) bool {
	r, ok := RulesFor(list)
	if !ok {
		return false
	}
	return r.Operations[op].Has(role)
}

// OwnerScoped 回報角色在該操作下是否僅限自己建立的資料。
func OwnerScoped(list string, op Op, role string) bool {
	r, ok := RulesFor(list)
	if !ok {
		return false
	}
	return r.OwnerScoped[op].Has(role)
}

// FieldReadAllowed 回報角色是否可讀取欄位；未設規則的欄位跟隨 list 權限。
func FieldReadAllowed(list, field, role string) bool {
	r, ok := RulesFor(list)
	if !ok {
		return true // 非 list 型別（payload 等）不在此管轄
	}
	rule, ok := r.Fields[field]
	if !ok || rule.Read == nil {
		return true
	}
	return rule.Read.Has(role)
}

// FieldWriteAllowed 回報角色是否可在 create/update 時寫入欄位。
func FieldWriteAllowed(list, field string, op Op, role string) bool {
	r, ok := RulesFor(list)
	if !ok {
		return false
	}
	rule, ok := r.Fields[field]
	if !ok {
		return true
	}
	var set RoleSet
	switch op {
	case OpCreate:
		set = rule.Create
	case OpUpdate:
		set = rule.Update
	}
	if set == nil {
		return true
	}
	return set.Has(role)
}
