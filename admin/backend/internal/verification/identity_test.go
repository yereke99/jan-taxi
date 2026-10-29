package verification

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func testService(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(nil, Options{Enabled: true, Key: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32)), AutoApprove: true, MinConfidence: 98})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func identityFixture() Snapshot {
	return Snapshot{Input: Input{DocumentType: "id_card", IIN: "999999999990", DocumentNumber: "001234567"}, FirstName: "ӘЛІ", LastName: "ҚАЛИЕВ"}
}
func TestIdentityEncryptionAndChecks(t *testing.T) {
	s := testService(t)
	in := identityFixture()
	a, err := s.seal(in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.seal(in)
	if bytes.Contains(a, []byte(in.IIN)) || bytes.Equal(a, b) {
		t.Fatal("plaintext identity or repeated nonce")
	}
	var out Snapshot
	if err = s.open(a, &out); err != nil || out != in {
		t.Fatal("roundtrip")
	}
	a[len(a)-1] ^= 1
	if s.open(a, &out) == nil {
		t.Fatal("tampering accepted")
	}
	if bytes.Equal(s.fingerprint("iin", "value"), s.fingerprint("document", "value")) {
		t.Fatal("lookup namespace collision")
	}
	if !ValidIIN(in.IIN) || ValidIIN("999999999991") || ValidIIN("１２３４５６７８９０１０") {
		t.Fatal("checksum or ASCII validation")
	}
	if NormalizeName("  Әлі  ҚАЛИЕВ ") != "ӘЛІ ҚАЛИЕВ" || NormalizeName("Қ") == NormalizeName("К") {
		t.Fatal("lossy Kazakh normalization")
	}
	licence, err := (Input{DocumentType: "driving_licence", IIN: in.IIN, DocumentNumber: " AA № 000012 "}).Normalize()
	if err != nil || licence.DocumentNumber != "AA000012" || !licence.Complete() {
		t.Fatal("licence series/zeros lost")
	}
}
func highConfidenceResult() Result {
	v := identityFixture()
	return Result{Extracted: v.Input, FirstName: v.FirstName, LastName: v.LastName, DocumentType: "id_card", Fields: map[string]float64{"document_type": 99, "first_name": 99, "last_name": 99, "iin": 99, "document_number": 99}}
}
func TestAssessmentNeverApprovesUncertainty(t *testing.T) {
	want := identityFixture()
	r := Assess(highConfidenceResult(), want, 98)
	if len(r.Reasons) != 0 {
		t.Fatal(r.Reasons)
	}
	low := highConfidenceResult()
	low.Fields["iin"] = 60
	r = Assess(low, want, 98)
	if r.Comparisons["iin"] != "uncertain" || len(r.Reasons) == 0 {
		t.Fatal("uncertainty treated as match")
	}
	mismatch := highConfidenceResult()
	mismatch.LastName = "КАЛИЕВ"
	r = Assess(mismatch, want, 98)
	if r.Comparisons["last_name"] != "mismatch" {
		t.Fatal("Kazakh letters folded")
	}
	optional := highConfidenceResult()
	optional.Extracted.Patronymic = ""
	if len(Assess(optional, want, 98).Reasons) != 0 {
		t.Fatal("patronymic required")
	}
	want.Patronymic = "ӘКЕСІҰЛЫ"
	if len(Assess(optional, want, 98).Reasons) == 0 {
		t.Fatal("entered patronymic not compared")
	}
}
func TestExtractLayoutsAndViewerFilename(t *testing.T) {
	lines := []Line{{"999999999990-2026102516224.png", 99}, {"УДОСТОВЕРЕНИЕ ЛИЧНОСТИ", 99}, {"ТЕГІ / ФАМИЛИЯ", 99}, {"ҚАЛИЕВ", 99}, {"АТЫ / ИМЯ", 99}, {"ӘЛІ", 99}, {"ӘКЕСІНІҢ АТЫ / ОТЧЕСТВО", 99}, {"ТУҒАН КҮНІ / ДАТА РОЖДЕНИЯ", 99}, {"ЖСН / ИИН 999999999990", 99}, {"001234567", 99}}
	r := Extract(lines)
	if r.Extracted.IIN != "999999999990" || r.Extracted.Patronymic != "" || r.Extracted.DocumentNumber != "001234567" {
		t.Fatalf("wrong field classification: %+v", r)
	}
	if len(Assess(r, identityFixture(), 98).Reasons) != 0 {
		t.Fatal("labelled physical card not matched")
	}
	licence := Extract([]Line{{"DRIVING LICENCE", 99}, {"1. ҚАЛИЕВ / QALIEV", 99}, {"2. ӘЛІ / ALI", 99}, {"4d) ЖСН / IIN 999999999990", 99}, {"5. AA № 000012", 99}})
	if licence.DocumentType != "driving_licence" || licence.Extracted.DocumentNumber != "AA000012" || licence.Extracted.IIN != "999999999990" {
		t.Fatal("licence number confused with IIN")
	}
	onlyFilename := Extract([]Line{{"999999999990-20261025.png", 99}})
	if onlyFilename.Extracted.IIN != "" {
		t.Fatal("viewer filename used as IIN")
	}
}
func TestParseTSVConfidence(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n5\t1\t1\t1\t1\t1\t0\t0\t10\t10\t99\tЖСН\n5\t1\t1\t1\t1\t2\t0\t0\t10\t10\t62\t999999999990\n"
	lines, err := ParseTSV(strings.NewReader(tsv))
	if err != nil || len(lines) != 1 || lines[0].Confidence != 62 {
		t.Fatal("word uncertainty lost")
	}
}

func TestFallbackCannotEraseUncertaintyOrConflicts(t *testing.T) {
	a, b := highConfidenceResult(), highConfidenceResult()
	a.Fields["iin"] = 62
	b.LastName = "БАСҚА"
	merged := MergeEvidence(a, b)
	if merged.Fields["iin"] != 62 || len(merged.Reasons) == 0 || len(Assess(merged, identityFixture(), 98).Reasons) == 0 {
		t.Fatal("fallback hid uncertainty or conflict")
	}
}
