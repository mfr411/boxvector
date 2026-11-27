package cmd

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mfr411/boxvector/pkg/web"
)

var (
	webListen     string
	webAuthFile   string
	webStateDir   string
	webTLSCert    string
	webTLSKey     string
	webTrustProxy bool
	webAllowAny   bool
)

func init() {
	webCmd.Flags().StringVar(&webListen, "listen", "0.0.0.0:6969", "listen address (host:port)")
	webCmd.Flags().StringVar(&webStateDir, "state-dir", "/var/lib/boxvector", "directory for snapshots and history")
	webCmd.Flags().StringVar(&webTLSCert, "tls-cert", "", "TLS certificate file (enables HTTPS)")
	webCmd.Flags().StringVar(&webTLSKey, "tls-key", "", "TLS key file")
	webCmd.Flags().BoolVar(&webTrustProxy, "trust-proxy", false, "trust X-Forwarded-For/Proto from a reverse proxy")
	webCmd.Flags().BoolVar(&webFileAuth, "file-auth", false, "sign in with --auth-file users (bcrypt) instead of the server accounts")
	webCmd.Flags().BoolVar(&webAllowAny, "allow-public", true, "allow listening on a non-loopback address without TLS")
	webCmd.PersistentFlags().StringVar(&webAuthFile, "auth-file", "/etc/boxvector/web.users", "user file (user:bcrypt per line, mode 600)")
	webCmd.AddCommand(webPasswdCmd)
	rootCmd.AddCommand(webCmd)
}

var webFileAuth bool

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Run the web UI",
	Run: func(cmd *cobra.Command, args []string) {
		host, _, err := net.SplitHostPort(webListen)
		if err != nil {
			log.Fatalf("listen: %s", err)
		}
		ip := net.ParseIP(host)
		loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
		if !loopback && webTLSCert == "" && !webAllowAny {
			log.Fatalf("refusing to serve plain HTTP on %s: use --tls-cert/--tls-key, a reverse proxy on 127.0.0.1, or --allow-public", webListen)
		}
		if (webTLSCert == "") != (webTLSKey == "") {
			log.Fatal("--tls-cert and --tls-key must be used together")
		}
		log.Infof("Boxvector web UI on %s (config %s)", webListen, configFile)
		err = web.Serve(web.Options{
			Listen: webListen, ConfigFile: configFile, AuthFile: webAuthFile, StateDir: webStateDir,
			TLSCert: webTLSCert, TLSKey: webTLSKey, TrustProxy: webTrustProxy, SystemAuth: !webFileAuth,
		})
		if err != nil {
			log.Fatal(err)
		}
	},
}

var webPasswdCmd = &cobra.Command{
	Use:   "passwd <user>",
	Short: "Create a web user or change its password",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var pw string
		if term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprint(os.Stderr, "Password: ")
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Fprint(os.Stderr, "Repeat:   ")
			b2, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				log.Fatal(err)
			}
			if string(b) != string(b2) {
				log.Fatal("passwords do not match")
			}
			pw = string(b)
		} else { // allow: echo "$PW" | boxvector web passwd admin
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			pw = strings.TrimRight(line, "\r\n")
		}
		if err := web.SetPassword(webAuthFile, args[0], pw); err != nil {
			log.Fatal(err)
		}
		log.Infof("user %s saved to %s", args[0], webAuthFile)
	},
}
