package i18n

import "testing"

func TestNormalizeAndMessages(t *testing.T) {
	defer SetDefault(LangPT)

	if got := Normalize("en"); got != LangEN {
		t.Errorf("Normalize(en) = %q, want %q", got, LangEN)
	}
	if got := Normalize("en-US"); got != LangEN {
		t.Errorf("Normalize(en-US) = %q, want %q", got, LangEN)
	}
	if got := Normalize("pt-BR"); got != LangPT {
		t.Errorf("Normalize(pt-BR) = %q, want %q", got, LangPT)
	}
	if got := Normalize(""); got != LangPT {
		t.Errorf("Normalize('') = %q, want %q", got, LangPT)
	}

	SetDefault("en")
	if !IsEN() {
		t.Errorf("expected IsEN() true after SetDefault(en)")
	}
	if got := Pick("Olá", "Hello"); got != "Hello" {
		t.Errorf("Pick in en = %q, want Hello", got)
	}
	if got := Current().DashboardTitle; got != "Operations Console" {
		t.Errorf("Current().DashboardTitle = %q, want Operations Console", got)
	}

	SetDefault("pt-BR")
	if IsEN() {
		t.Errorf("expected IsEN() false after SetDefault(pt-BR)")
	}
	if got := Pick("Olá", "Hello"); got != "Olá" {
		t.Errorf("Pick in pt-BR = %q, want Olá", got)
	}
}
