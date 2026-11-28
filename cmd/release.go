package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mfr411/boxvector/pkg/monitor"
)

var releaseKey string

func init() {
	releaseCmd.PersistentFlags().StringVar(&releaseKey, "key", "", "private release key file")
	releaseCmd.AddCommand(releaseKeygenCmd, releaseSignCmd, releaseVerifyCmd)
	rootCmd.AddCommand(releaseCmd)
}

var releaseCmd = &cobra.Command{Use: "release", Short: "Sign and verify release packages (maintainers)"}

var releaseKeygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Create a release signing key; prints the public key to embed in the agent",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if releaseKey == "" {
			return fmt.Errorf("--key <file> is required")
		}
		if _, err := os.Stat(releaseKey); err == nil {
			return fmt.Errorf("%s already exists, not overwriting", releaseKey)
		}
		pub, err := monitor.KeyGen(releaseKey)
		if err != nil {
			return err
		}
		fmt.Println(pub)
		return nil
	},
}

var releaseSignCmd = &cobra.Command{
	Use:   "sign <package.deb>",
	Short: "Write <package.deb>.sig",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if releaseKey == "" {
			return fmt.Errorf("--key <file> is required")
		}
		sig, err := monitor.SignDeb(releaseKey, args[0])
		if err != nil {
			return err
		}
		return os.WriteFile(args[0]+".sig", []byte(sig+"\n"), 0o644)
	},
}

var releaseVerifyCmd = &cobra.Command{
	Use:   "verify <package.deb>",
	Short: "Check <package.deb>.sig against the trusted release keys",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sig, err := os.ReadFile(args[0] + ".sig")
		if err != nil {
			return err
		}
		if err := monitor.VerifyDebFile(args[0], string(sig)); err != nil {
			return err
		}
		fmt.Println("OK: signature valid")
		return nil
	},
}
