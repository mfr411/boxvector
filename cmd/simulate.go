package cmd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/mfr411/boxvector/pkg/bird"
	"github.com/mfr411/boxvector/pkg/process"
	"github.com/mfr411/boxvector/pkg/simulate"
)

var (
	birdBinary   string
	birdSocket   string
	simWait      time.Duration
	simKeep      bool
	simPeerAS    uint32
	simNeighbors []string
)

func init() {
	simulateCmd.Flags().StringVar(&birdBinary, "bird-binary", "bird", "BIRD binary used to generate and inspect the config")
	simulateCmd.Flags().StringVar(&birdSocket, "bird-socket", "/run/bird/bird.ctl", "Control socket of the running BIRD instance")
	simulateCmd.Flags().DurationVar(&simWait, "wait", 20*time.Second, "Time to wait for BIRD to settle before reading tables")
	simulateCmd.Flags().BoolVar(&simKeep, "keep", false, "Keep the temporary instance running")
	simulateCmd.Flags().Uint32Var(&simPeerAS, "peer-as", 64999, "ASN used for unspecified simulated peers")
	simulateCmd.Flags().StringSliceVar(&simNeighbors, "neighbor", nil, "Only simulate these neighbor addresses")
	rootCmd.AddCommand(simulateCmd)
}

var simulateCmd = &cobra.Command{
	Use:   "simulate [peer:file ...]",
	Short: "Announce test routes to a running BIRD and check what its filters did",
	Long: `Generates the configuration into a throwaway directory, starts BIRD on a
separate socket, then opens one BGP session per simulated peer and announces the
routes from its file.

Route file format, one per line:

	<accept|reject|filtered> <prefix> [aspath=AS,AS] [comm=ASN:VAL,...]

Every line states the expected outcome, so a mismatch is reported as a failure.
Example:

	accept 203.0.113.0/24 aspath=64999,64500
	reject 198.51.100.0/24 prefix-len=24`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		root, err := os.MkdirTemp("", "boxvector-simulate")
		if err != nil {
			log.Fatal(err)
		}
		if !simKeep {
			defer os.RemoveAll(root)
		}

		// Point every runtime path at the throwaway directory so the host
		// instance is never touched.
		cfg, err := os.ReadFile(configFile)
		if err != nil {
			log.Fatal(err)
		}
		patched := filepath.Join(root, "boxvector.yml")
		extra := fmt.Sprintf("\nbird-directory: %s/bird/\nbird-binary: %s\nbird-socket: %s/bird.ctl\ncache-directory: %s/cache\nweb-ui-file: %s/ui.html\nkeepalived-config: %s/keepalived.conf\n",
			root, birdBinary, root, root, root, root)
		if err = os.WriteFile(patched, append(cfg, []byte(extra)...), 0o600); err != nil {
			log.Fatal(err)
		}
		for _, d := range []string{"bird", "cache"} {
			if err = os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
				log.Fatal(err)
			}
		}
		process.Run(patched, "", version, true, false, false)

		birdConf := filepath.Join(root, "bird", "bird.conf")
		if _, err = os.Stat(birdConf); err != nil {
			log.Fatal("no generated config, check the above errors")
		}
		if err = os.Rename(birdConf, filepath.Join(root, "bird", "bird.conf")); err != nil {
			log.Fatal(err)
		}
		bird.Validate(birdBinary, filepath.Join(root, "bird")) // exits on failure

		birdCmd := exec.Command(birdBinary, "-f", "-c", birdConf, "-s", filepath.Join(root, "bird.ctl"))
		birdCmd.Stdout = os.Stderr
		birdCmd.Stderr = os.Stderr
		if err = birdCmd.Start(); err != nil {
			log.Fatal(err)
		}
		defer func() {
			birdCmd.Process.Kill()
			birdCmd.Wait()
		}()
		waitForSocket(filepath.Join(root, "bird.ctl"))

		var peerAS uint32
	sessions := make([]simulate.Session, 0, len(args))
		for _, arg := range args {
			name, file, ok := cutPeer(arg)
			if !ok {
				log.Fatalf("%s: want peer:file", arg)
			}
			neighbors, ok := peers()[name]
			if !ok {
				log.Fatalf("%s: unknown peer", name)
			}
			if owner, ok := owners()[name]; ok {
				peerAS = owner
			} else {
				peerAS = simPeerAS
			}
			routes, err := simulate.ReadFile(file, peerAS)
			if err != nil {
				log.Fatal(err)
			}
			for _, n := range neighbors {
				if len(simNeighbors) > 0 && !contains(simNeighbors, n) {
					continue
				}
				sessions = append(sessions, simulate.Session{
					Name: name, Neighbor: net.JoinHostPort(n, "179"), LocalAS: localASN(), PeerAS: peerAS, Routes: routes,
				})
			}
		}

		res, err := simulate.Run(simulate.Options{
			BirdBinary: birdBinary,
			BirdSocket: filepath.Join(root, "bird.ctl"),
			Wait:       simWait,
			Reporter:   func(s string) { fmt.Println("FAIL " + s) },
		}, sessions)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(res)
		if !res.Passed {
			os.Exit(1)
		}
	},
}

// cutPeer splits "peer:file", allowing a colon inside file paths.
func cutPeer(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func waitForSocket(path string) {
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Fatal("BIRD did not start")
}

// peers returns the neighbor addresses of every configured peer.
func peers() map[string][]string {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	out := map[string][]string{}
	for name, p := range c.Peers {
		if p.NeighborIPs != nil {
			out[name] = *p.NeighborIPs
		}
	}
	return out
}

// owners returns the configured ASN of every peer.
func owners() map[string]uint32 {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	out := map[string]uint32{}
	for name, p := range c.Peers {
		if p.ASN != nil {
			out[name] = uint32(*p.ASN)
		}
	}
	return out
}

// localASN returns the ASN the generated config uses locally.
func localASN() uint32 {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	return uint32(c.ASN)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
