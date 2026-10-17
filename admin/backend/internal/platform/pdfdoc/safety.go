package pdfdoc

import "sort"

// activeKeys are dictionary keys (and action types under /S) that make a
// document run code, launch programs, carry attachments or submit data.
var activeKeys = map[Name]bool{
	"JavaScript": true, "JS": true, "Launch": true, "EmbeddedFile": true, "EmbeddedFiles": true,
	"RichMedia": true, "XFA": true, "SubmitForm": true, "ImportData": true, "GoToE": true,
	"Sound": true, "Movie": true, "Rendition": true,
}

// ActiveContent lists the active-content features found anywhere in the
// document (keys or action types), sorted. A receipt must have none.
func (d *Doc) ActiveContent() []string {
	found := map[string]bool{}
	nodes := 0
	var walk func(o Object, depth int)
	walk = func(o Object, depth int) {
		nodes++
		if depth > 32 || nodes > 2_000_000 {
			return
		}
		switch v := o.(type) {
		case Dict:
			for k, val := range v {
				if activeKeys[k] {
					found[string(k)] = true
				}
				if k == "S" {
					if n, ok := val.(Name); ok && activeKeys[n] {
						found[string(n)] = true
					}
				}
				walk(val, depth+1)
			}
		case Array:
			for _, val := range v {
				walk(val, depth+1)
			}
		case *Stream:
			walk(v.Dict, depth+1)
		}
	}
	for _, e := range d.objects {
		walk(e.obj, 0)
	}
	for _, t := range d.trls {
		walk(t.dict, 0)
	}
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HasForm reports an interactive form (AcroForm) in the catalog.
func (d *Doc) HasForm() bool {
	root := d.Root()
	return root != nil && root["AcroForm"] != nil
}

// HasXMPMetadata reports an XMP metadata stream on the catalog.
func (d *Doc) HasXMPMetadata() bool {
	root := d.Root()
	return root != nil && root["Metadata"] != nil
}
