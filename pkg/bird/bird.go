package bird

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/mfr411/boxvector/pkg/util"
)

// Minimum supported BIRD version
const supportedMin = "2.0.7"

// isNumeric checks if a byte is character for number
func isNumeric(b byte) bool {
	return b >= byte('0') && b <= byte('9')
}

func read(r io.Reader, w io.Writer) bool {
	// Read from socket byte by byte, until reaching newline character
	c := make([]byte, 1024)
	pos := 0
	for {
		if pos >= 1024 {
			break
		}
		_, err := r.Read(c[pos : pos+1])
		if err != nil {
			panic(err)
		}
		if c[pos] == byte('\n') {
			break
		}
		pos++
	}

	c = c[:pos+1]

	// Remove preceding status numbers
	if pos > 4 && isNumeric(c[0]) && isNumeric(c[1]) && isNumeric(c[2]) && isNumeric(c[3]) {
		// There is a status number at beginning, remove it (first 5 bytes)
		if w != nil && pos > 6 {
			pos = 5
			if _, err := w.Write(c[pos:]); err != nil {
				panic(err)
			}
		}
		return c[0] != byte('0') && c[0] != byte('8') && c[0] != byte('9')
	} else {
		if w != nil {
			if _, err := w.Write(c[1:]); err != nil {
				panic(err)
			}
		}
		return true
	}
}

// Read reads the full BIRD response as a string
func Read(r io.Reader) (string, error) {
	var buf bytes.Buffer
	for read(r, &buf) {
	}
	if r := recover(); r != nil {
		return "", fmt.Errorf("%s", r)
	}
	return buf.String(), nil
}

// ReadClean reads from the provided reader and trims unneeded whitespace and bird 4-digit numbers
func ReadClean(r io.Reader) {
	resp, err := Read(r)
	if err != nil {
		return
	}

	reg := regexp.MustCompile(`[0-9]{4}-? ?`)
	resp = reg.ReplaceAllString(resp, "")
	resp = strings.ReplaceAll(resp, "\n ", "\n")
	resp = strings.ReplaceAll(resp, "\n\n", "\n")
	resp = strings.TrimSuffix(resp, "\n")

	fmt.Println(resp)
}

// RunCommand runs a BIRD command and returns the output, version, and error
func RunCommand(command string, socket string) (string, string, error) {
	log.Debugln("Connecting to BIRD socket")
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return "", "", err
	}
	//noinspection GoUnhandledErrorResult
	defer conn.Close()

	log.Debug("Connected to BIRD socket")
	resp, err := Read(conn)
	if err != nil {
		return "", "", err
	}
	log.Debugf("BIRD init response: %s", resp)

	// Check BIRD version
	birdVersion := strings.Split(resp, " ")[1]
	if !AtLeast(birdVersion, supportedMin) {
		log.Warnf("BIRD version %s older than minimum supported version %s", birdVersion, supportedMin)
	}

	log.Debugf("Sending BIRD command: %s", command)
	_, err = conn.Write([]byte(strings.Trim(command, "\n") + "\n"))
	log.Debugf("Sent BIRD command: %s", command)
	if err != nil {
		return "", "", err
	}

	log.Debugln("Reading from socket")
	resp, err = Read(conn)
	if err != nil {
		return "", "", err
	}
	log.Debugln("Done reading from socket")

	return resp, birdVersion, nil // nil error
}

// reValidationError matches "<file>:<line>:<col> <message>" in BIRD 2 and BIRD 3 output
var reValidationError = regexp.MustCompile(`(?m)(\S+\.conf):(\d+):(\d+)\s+(.*)$`)

var reVersion = regexp.MustCompile(`BIRD version (\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion returns major, minor, patch from "2.20.0" or "BIRD version 3.4.0".
func ParseVersion(s string) (int, int, int, bool) {
	if !strings.Contains(s, "BIRD version") {
		s = "BIRD version " + strings.TrimPrefix(strings.TrimSpace(s), "v")
	}
	m := reVersion.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, 0, false
	}
	ma, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	pa, _ := strconv.Atoi(m[3])
	return ma, mi, pa, true
}

// AtLeast reports whether version v >= min (numeric compare; the previous
// semver check never fired because BIRD versions have no "v" prefix).
func AtLeast(v, min string) bool {
	a1, b1, c1, ok1 := ParseVersion(v)
	a2, b2, c2, ok2 := ParseVersion(min)
	if !ok1 || !ok2 {
		return true // unknown: do not warn spuriously
	}
	if a1 != a2 {
		return a1 > a2
	}
	if b1 != b2 {
		return b1 > b2
	}
	return c1 >= c2
}

// BinaryVersion runs "<binary> --version" and returns e.g. "3.4.0".
func BinaryVersion(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if m := reVersion.FindString(string(out)); m != "" {
		return strings.TrimPrefix(m, "BIRD version "), nil
	}
	if err == nil {
		err = fmt.Errorf("unexpected output %q", strings.TrimSpace(string(out)))
	}
	return "", err
}

// Validate checks if the cached configuration is syntactically valid
func Validate(binary string, cacheDir string) {
	log.Debugf("Validating BIRD config")
	var outb, errb bytes.Buffer
	birdCmd := exec.Command(binary, "-c", "bird.conf", "-p")
	birdCmd.Dir = cacheDir
	birdCmd.Stdout = &outb
	birdCmd.Stderr = &errb
	var errbT string
	if err := birdCmd.Run(); err != nil {
		origErr := err
		errbT = strings.TrimSuffix(errb.String(), "\n")

		// Check for validation error. BIRD 2 and BIRD 3 differ:
		//   BIRD 2: bird: ./AS65530_EXAMPLE.conf:20:43 syntax error, unexpected '%'
		//   BIRD 3: bird: 2026-01-01 00:00:00.000 [0000] <FATAL> ./AS65530_EXAMPLE.conf:20:43 syntax error, ...
		errorMessageToLog := errbT
		if m := reValidationError.FindStringSubmatch(errbT); m != nil {
			errorMessageToLog = "BIRD validation error:\n" // Clear error message so we can write the new nicely formatted one
			errorFile, errorMessage := m[1], m[4]
			errorLine, _ := strconv.Atoi(m[2]) // regex guarantees digits
			errorChar, _ := strconv.Atoi(m[3])
			log.Debugf("Found error in %s:%d:%d message %s", errorFile, errorLine, errorChar, errorMessage)

			// Read output file
			file, err := os.Open(path.Join(cacheDir, errorFile))
			if err != nil {
				log.Fatalf("unable to read BIRD output file for error parsing: %s", err)
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			line := 1
			for scanner.Scan() {
				if (line >= errorLine-1) && (line <= errorLine+1) { // Print one line above and below the error line
					errorMessageToLog += scanner.Text() + "\n"
				}
				if line == errorLine {
					errorMessageToLog += strings.Repeat(" ", errorChar-1) + "^ " + errorMessage + "\n"
				}
				line++
			}
			if err := scanner.Err(); err != nil {
				log.Fatalf("BIRD output file scan: %s", err)
			}
		}
		if errorMessageToLog == "" {
			errorMessageToLog = origErr.Error()
		}
		log.Fatalf("BIRD: %s\n", errorMessageToLog)
	}

	log.Infof("BIRD config validation passed")
}

// MoveCacheAndReconfigure moves cached files to the production BIRD directory and reconfigures
func MoveCacheAndReconfigure(birdDirectory string, cacheDirectory string, birdSocket string, noConfigure bool) {
	// Remove old configs
	birdConfigFiles, err := filepath.Glob(path.Join(birdDirectory, "AS*.conf"))
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range birdConfigFiles {
		log.Debugf("Removing old BIRD config file %s", f)
		if err := os.Remove(f); err != nil {
			log.Fatalf("Removing old BIRD config files: %v", err)
		}
	}

	// Copy from cache to bird config
	files, err := filepath.Glob(path.Join(cacheDirectory, "*.conf"))
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		fileNameParts := strings.Split(f, "/")
		fileNameTail := fileNameParts[len(fileNameParts)-1]
		newFileLoc := path.Join(birdDirectory, fileNameTail)
		log.Debugf("Moving %s to %s", f, newFileLoc)
		if err := util.MoveFile(f, newFileLoc); err != nil {
			log.Fatalf("Moving cache file to bird directory: %v", err)
		}
	}

	// Move config file
	log.Debug("Moving Pathvector config file")
	configFilename := "pathvector.yml"
	if err := util.MoveFile(
		path.Join(cacheDirectory, configFilename),
		path.Join(birdDirectory, configFilename),
	); err != nil {
		log.Fatalf("Moving pathvector config file: %v", err)
	}

	if !noConfigure {
		log.Info("Reconfiguring BIRD")
		resp, _, err := RunCommand("configure", birdSocket)
		if err != nil {
			log.Fatal(err)
		}
		// Print bird output as multiple lines
		for _, line := range strings.Split(strings.Trim(resp, "\n"), "\n") {
			log.Printf("BIRD response (multiline): %s", line)
		}
	}
}

// Reformat takes a BIRD config file as a string and outputs a nicely formatted version as a string
func Reformat(input string) string {
	formatted := ""
	for _, line := range strings.Split(input, "\n") {
		if strings.HasSuffix(line, "{") || strings.HasSuffix(line, "[") {
			formatted += "\n"
		}

		if !func(input string) bool {
			for _, chr := range input {
				if string(chr) != " " {
					return false
				}
			}
			return true
		}(line) {
			formatted += line + "\n"
		}
	}
	return formatted
}

type Routes struct {
	Imported  int
	Filtered  int
	Exported  int
	Preferred int
}

type BGPState struct {
	NeighborAddress string
	NeighborAS      int
	LocalAS         int
	NeighborID      string
}

type ProtocolState struct {
	Name   string
	Proto  string
	Table  string
	State  string
	Since  string
	Info   string
	Routes *Routes
	BGP    *BGPState
}

func trimRepeatingSpace(s string) string {
	space := regexp.MustCompile(`\s+`)
	return space.ReplaceAllString(s, " ")
}

// trimDupSpace trims duplicate whitespace
func trimDupSpace(s string) string {
	headTailWhitespace := regexp.MustCompile(`^[\s\p{Zs}]+|[\s\p{Zs}]+$`)
	innerWhitespace := regexp.MustCompile(`[\s\p{Zs}]{2,}`)
	return innerWhitespace.ReplaceAllString(headTailWhitespace.ReplaceAllString(s, ""), " ")
}

func parseBGP(s string) (*BGPState, error) {
	out := &BGPState{
		NeighborAddress: "",
		NeighborAS:      -1,
		LocalAS:         -1,
		NeighborID:      "",
	}

	if !strings.Contains(s, "BGP state:") {
		return nil, nil
	}

	addressRegex := regexp.MustCompile(`(.*)Neighbor address:(.*)`)
	address := trimRepeatingSpace(
		trimDupSpace(
			addressRegex.FindString(s),
		),
	)
	out.NeighborAddress = strings.Split(address, "Neighbor address: ")[1]

	neighborASRegex := regexp.MustCompile(`(.*)Neighbor AS:(.*)`)
	neighborAS := trimRepeatingSpace(
		trimDupSpace(
			neighborASRegex.FindString(s),
		),
	)
	neighborAS = strings.Split(neighborAS, "Neighbor AS: ")[1]
	neighborASInt, err := strconv.ParseInt(neighborAS, 10, 32)
	if err != nil {
		return nil, err
	}
	out.NeighborAS = int(neighborASInt)

	localASRegex := regexp.MustCompile(`(.*)Local AS:(.*)`)
	localAS := trimRepeatingSpace(
		trimDupSpace(
			localASRegex.FindString(s),
		),
	)
	localAS = strings.Split(localAS, "Local AS: ")[1]
	localASInt, err := strconv.ParseInt(localAS, 10, 32)
	if err != nil {
		return nil, err
	}
	out.LocalAS = int(localASInt)

	neighborIDRegex := regexp.MustCompile(`(.*)Neighbor ID:(.*)`)
	neighborID := trimRepeatingSpace(
		trimDupSpace(
			neighborIDRegex.FindString(s),
		),
	)
	neighborIDParts := strings.Split(neighborID, "Neighbor ID: ")
	if len(neighborIDParts) > 1 {
		out.NeighborID = neighborIDParts[1]
	}

	return out, nil
}

func parseRoutes(s string) (*Routes, error) {
	out := &Routes{
		Imported:  -1,
		Filtered:  -1,
		Exported:  -1,
		Preferred: -1,
	}

	routesRegex := regexp.MustCompile(`(.*)Routes:(.*)`)
	routes := routesRegex.FindString(s)
	routes = trimDupSpace(routes)
	routes = trimRepeatingSpace(routes)

	routeTokens := strings.Split(routes, "Routes: ")
	if len(routeTokens) < 2 {
		return out, nil
	}

	routesParts := strings.Split(routeTokens[1], ", ")

	for r := range routesParts {
		parts := strings.Split(routesParts[r], " ")
		num, err := strconv.ParseInt(parts[0], 10, 32)
		if err != nil {
			return nil, err
		}
		switch parts[1] {
		case "imported":
			out.Imported = int(num)
		case "filtered":
			out.Filtered = int(num)
		case "exported":
			out.Exported = int(num)
		case "preferred":
			out.Preferred = int(num)
		}
	}

	return out, nil
}

// ParseProtocol parses a single protocol
func ParseProtocol(p string) (*ProtocolState, error) {
	p = noWhitespace(p)

	// Remove lines that start with BIRD
	birdRegex := regexp.MustCompile(`BIRD.*ready.*`)
	p = birdRegex.ReplaceAllString(p, "")
	tableHeaderRegex := regexp.MustCompile(`Name.*Info`)
	p = tableHeaderRegex.ReplaceAllString(p, "")

	// Remove control characters
	ccRegex := regexp.MustCompile(`\d\d\d\d-\w?$`)
	p = ccRegex.ReplaceAllString(p, "")

	// Remove leading and trailing newlines
	p = strings.Trim(p, "\n")
	header := strings.Split(p, "\n")[0]
	header = trimRepeatingSpace(header)
	headerParts := strings.Split(header, " ")

	if len(headerParts) < 5 {
		return nil, fmt.Errorf("%s\ninvalid header len %d: %+v (%s)", p, len(headerParts), headerParts, header)
	}

	// Parse since field - there are multiple possible formats here
	var since, info string
	if strings.Contains(headerParts[4], ".") { // Combined time/date
		since = headerParts[4]
		info = strings.Join(headerParts[5:], " ")
	} else { // Split time/date
		since = headerParts[4] + " " + headerParts[5]
		info = strings.Join(headerParts[6:], " ")
	}

	// Parse header
	protocolState := &ProtocolState{
		Name:  headerParts[0],
		Proto: headerParts[1],
		Table: headerParts[2],
		State: headerParts[3],
		Since: since,
		Info:  trimDupSpace(info),
	}

	routes, err := parseRoutes(p)
	if err != nil {
		return nil, err
	}
	protocolState.Routes = routes

	bgp, err := parseBGP(p)
	if err != nil {
		return nil, err
	}
	protocolState.BGP = bgp

	return protocolState, nil
}

// noWhitespace removes all leading and trailing whitespace from every line
func noWhitespace(p string) string {
	p = strings.Trim(p, "\n")
	lines := strings.Split(p, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(trimRepeatingSpace(line))
	}
	return strings.Join(lines, "\n")
}

// ParseProtocols parses a list of protocols
func ParseProtocols(p string) ([]*ProtocolState, error) {
	p = noWhitespace(p)
	protocols := strings.Split(p, "\n\n")
	protocolStates := make([]*ProtocolState, len(protocols))
	for i, protocol := range protocols {
		protocolState, err := ParseProtocol(protocol)
		if err != nil {
			return nil, err
		}
		protocolStates[i] = protocolState
	}
	return protocolStates, nil
}
