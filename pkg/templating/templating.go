package templating

import (
	"embed"
	"fmt"
	htmltemplate "html/template"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/mfr411/boxvector/pkg/config"
	"github.com/mfr411/boxvector/pkg/util"
)

var (
	protocolNames       []string
	protocolNameMap     = map[string]*Protocol{} // bird name:protocol
	protocolNameMapLock = sync.Mutex{}
)

// Wrapper is passed to the peer template
type Wrapper struct {
	Name   string
	Peer   config.Peer
	Config config.Config
}

type Protocol struct {
	Name string
	Tags []string
}

// ProtocolNames gets a map of protocol names to user defined names
func ProtocolNames() map[string]*Protocol {
	return protocolNameMap
}

// BirdQuote makes a value safe between "..." in a BIRD config. BIRD string
// literals have no escape sequences (verified on 2.20 and 3.4), so a double
// quote becomes a single quote and control characters are dropped.
func BirdQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteRune('\'')
		case r < 0x20 || r == 0x7f:
			// control characters (newline, tab, ...) are never valid here
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Template functions
var funcMap = template.FuncMap{
	// Q escapes a value for use inside a double-quoted BIRD string so that
	// config input can never close the string and inject statements.
	"Q": BirdQuote,
	// C makes a value safe for a single-line "# ..." comment.
	"C":        func(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) },
	"Contains": strings.Contains,

	"Iterate": func(count *int) []int {
		// Create array with `count` entries
		var i int
		var items []int
		for i = 0; i < (*count); i++ {
			items = append(items, i)
		}
		return items
	},

	"BirdSet": func(prefixes []string) string {
		// Build a formatted BIRD prefix list
		output := ""
		for i, prefix := range prefixes {
			output += "  " + prefix
			if i != len(prefixes)-1 {
				output += ",\n"
			}
		}

		return output
	},

	"BirdASSet": func(asns []uint32) string {
		// Build a formatted BIRD AS set
		output := ""
		for i, prefix := range asns {
			output += fmt.Sprintf("  %d", prefix)
			if i != len(asns)-1 {
				output += ",\n"
			}
		}

		return output
	},

	"Empty": func(arr *[]string) bool {
		// Is `arr` empty?
		return arr == nil || len(*arr) == 0
	},

	"Timestamp": func(format string) string {
		// Get current timestamp
		if format == "unix" {
			return strconv.Itoa(int(time.Now().Unix()))
		}
		return time.Now().String()
	},

	"MakeSlice": func(args ...interface{}) []interface{} {
		return args
	},

	"IntCmp": func(i *int, j int) bool {
		return *i == j
	},

	"StringSliceIter": func(slice *[]string) []string {
		if slice != nil {
			return *slice
		}
		return []string{}
	},

	"Uint32SliceDeref": func(slice *[]uint32) []uint32 {
		if slice != nil {
			return *slice
		}
		return []uint32{}
	},

	"StrDeref": func(i *string) string {
		if i != nil {
			return *i
		}
		return ""
	},

	"BoolDeref": func(i *bool) bool {
		if i != nil {
			return *i
		}
		return false
	},

	"IntDeref": func(i *int) int {
		if i != nil {
			return *i
		}
		return 0
	},

	"UintDeref": func(i *uint) uint {
		if i != nil {
			return *i
		}
		return 0
	},

	"MapDeref": func(m *map[string]string) map[string]string {
		if m != nil {
			return *m
		}
		return map[string]string{}
	},

	"Uint32MapDeref": func(m *map[uint32]uint32) map[uint32]uint32 {
		if m != nil {
			return *m
		}
		return map[uint32]uint32{}
	},

	"StrSliceMapDeref": func(m *map[string][]string) map[string][]string {
		if m != nil {
			return *m
		}
		return map[string][]string{}
	},

	"Uint32SliceMapDeref": func(m *map[uint32][]uint32) map[uint32][]uint32 {
		if m != nil {
			return *m
		}
		return map[uint32][]uint32{}
	},

	"StringUint32MapDeref": func(m *map[string]uint32) map[string]uint32 {
		if m != nil {
			return *m
		}
		return map[string]uint32{}
	},

	"StrSliceDeref": func(s *[]string) []string {
		if s != nil {
			return *s
		}
		return []string{}
	},

	"StrSliceJoin": func(s *[]string) string {
		if s != nil {
			return strings.Join(*s, ", ")
		}
		return ""
	},

	// UniqueProtocolName takes a protocol-safe string and address family and returns a unique protocol name
	"UniqueProtocolName": func(s, userSuppliedName *string, af string, asn *int, tags *[]string) string {
		protoName := fmt.Sprintf("%s_AS%d_v%s", *s, *asn, af)
		i := 1
		// Peers are templated concurrently: check-and-reserve must be atomic,
		// otherwise two peers can get the same BIRD protocol name.
		protocolNameMapLock.Lock()
		defer protocolNameMapLock.Unlock()
		for {
			if !util.Contains(protocolNames, protoName) {
				protocolNames = append(protocolNames, protoName)
				var t []string
				if tags != nil {
					t = *tags
				}
				protocolNameMap[protoName] = &Protocol{
					Name: *userSuppliedName,
					Tags: t,
				}
				return protoName
			}
			protoName = fmt.Sprintf("%s_AS%d_v%s_%d", *s, *asn, af, i)
			i++
		}
	},

	"SplitFirst": func(s string, delim string) string {
		return strings.Split(s, delim)[0]
	},

	"Last": func(index, len int) bool {
		return index+1 == len
	},

	"U32MapContains": func(i int, m map[uint32][]uint32) bool {
		_, ok := m[uint32(i)]
		return ok
	},

	"ASPAFilter": func(asn int, aspa map[uint32][]uint32) string {
		if providers, ok := aspa[uint32(asn)]; ok {
			var out string
			for i, provider := range providers {
				out += fmt.Sprintf("bgp_path ~ [= * %d %d * =]", provider, asn)
				if i != len(providers)-1 {
					out += " || "
				}
			}
			return fmt.Sprintf(`if !((bgp_path ~ [= %d+ =]) || (%s)) then _reject("not in authorized providers list");`, asn, out)
		}
		return "# CODE ERROR: ASN not in ASPA map. This should never happen."
	},

	"ASSet": func(asns []uint32) string {
		out := "["
		for i, asn := range asns {
			out += fmt.Sprintf("%d", asn)
			if i != len(asns)-1 {
				out += ", "
			}
		}
		return out + "]"
	},
}

// Templates

var Template *template.Template

// uiTemplate is rendered with html/template so peer data is HTML-escaped (no XSS)
var uiTemplate *htmltemplate.Template

// Load loads the templates from the embedded filesystem
func Load(fs embed.FS) error {
	var err error
	Template, err = template.New("").Funcs(funcMap).ParseFS(fs, "templates/*.tmpl")
	if err != nil {
		return err
	}
	uiTemplate, err = htmltemplate.New("").Funcs(htmltemplate.FuncMap(funcMap)).ParseFS(fs, "templates/ui.tmpl")
	return err
}

// WriteVRRPConfig writes the VRRP config to a keepalived config file
func WriteVRRPConfig(instances map[string]*config.VRRPInstance, keepalivedConfig string) {
	if len(instances) < 1 {
		log.Debug("No VRRP instances are defined, not writing config")
		return
	}

	// Create the VRRP config file
	keepalivedFile, err := os.Create(keepalivedConfig)
	if err != nil {
		log.Fatalf("Create keepalived output file: %v", err)
	}

	// Render the template and write to disk
	if err := Template.ExecuteTemplate(keepalivedFile, "vrrp.tmpl", instances); err != nil {
		log.Fatalf("Execute template: %v", err)
	}
}

// WriteUIFile renders and writes the web UI file
func WriteUIFile(config *config.Config) {
	// Create the UI output file
	log.Debug("Creating UI output file")
	uiFileObj, err := os.Create(config.WebUIFile)
	if err != nil {
		log.Fatalf("Create UI output file: %v", err)
	}
	log.Debug("Finished creating UI file")

	// Render the UI template and write to disk
	log.Debug("Writing UI file")
	if err := uiTemplate.ExecuteTemplate(uiFileObj, "ui.tmpl", config); err != nil {
		log.Fatalf("Execute UI template: %v", err)
	}
	log.Debug("Finished writing UI file")
}
