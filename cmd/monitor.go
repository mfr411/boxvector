package cmd

import (
	"fmt"
	"net/http"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mfr411/boxvector/pkg/monitor"
)

var (
	monListen string
	monData   string
	monAuth   string
	agURL     string
	agToken   string
	agFile    string
)

func init() {
	monitorCmd.Flags().StringVar(&monListen, "listen", "127.0.0.1:8195", "listen address (put a TLS reverse proxy in front)")
	monitorCmd.PersistentFlags().StringVar(&monData, "data", "/var/lib/boxvector-monitor", "data directory (state, releases)")
	monitorCmd.PersistentFlags().StringVar(&monAuth, "auth-file", "/etc/boxvector-monitor/users", "admin users file (user:bcrypt)")
	monitorCmd.AddCommand(monitorPasswdCmd)
	agentEnrollCmd.Flags().StringVar(&agURL, "url", "https://monitor.boxvector.co", "monitor URL (https only)")
	agentEnrollCmd.Flags().StringVar(&agToken, "token", "", "enroll token")
	agentCmd.PersistentFlags().StringVar(&agFile, "agent-file", monitor.AgentFile, "agent identity file")
	agentCmd.AddCommand(agentEnrollCmd, agentRunCmd)
	rootCmd.AddCommand(monitorCmd, agentCmd)
}

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Run the central fleet monitor (heartbeats and patching)",
	Run: func(cmd *cobra.Command, args []string) {
		s, err := monitor.NewServer(monData, monAuth)
		if err != nil {
			log.Fatal(err)
		}
		srv := &http.Server{Addr: monListen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 10 * time.Minute, IdleTimeout: 2 * time.Minute}
		log.Infof("boxvector monitor on %s (data %s)", monListen, monData)
		log.Fatal(srv.ListenAndServe())
	},
}

var monitorPasswdCmd = &cobra.Command{
	Use:   "passwd <user>",
	Short: "Create a monitor admin or change its password",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprint(os.Stderr, "Password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil || len(b) < 10 {
			log.Fatal("password must be at least 10 characters")
		}
		_ = os.MkdirAll("/etc/boxvector-monitor", 0o750)
		if err := monitor.SetPassword(monAuth, args[0], string(b)); err != nil {
			log.Fatal(err)
		}
		fmt.Println("saved")
	},
}

var agentCmd = &cobra.Command{Use: "agent", Short: "Heartbeat agent for the central monitor"}

var agentEnrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Register this device with the monitor",
	Run: func(cmd *cobra.Command, args []string) {
		if agToken == "" {
			log.Fatal("--token is required")
		}
		if err := monitor.Enroll(agURL, agToken, agFile); err != nil {
			log.Fatalf("enroll failed: %s", err)
		}
		fmt.Println("enrolled; identity saved to", agFile)
	},
}

var agentRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Send heartbeats and apply approved patches",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := monitor.LoadAgent(agFile)
		if err != nil {
			log.Fatal(err)
		}
		monitor.RunAgent(c, version, configFile)
	},
}
