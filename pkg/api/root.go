// Package api is the DistroFace client and dfcli command tree
package api

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewRootCmd(version string) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "dfcli",
		Short:         "DistroFace CLI",
		Long:          `Command line interface for DistroFace registry and artifact management`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initClient()
		},
	}

	viper.SetDefault("server", defaultServerURL)
	viper.SetDefault("timeout", "5m")

	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ~/.dfcli/config.json)")
	rootCmd.PersistentFlags().String("server", defaultServerURL, "DistroFace server URL")
	rootCmd.PersistentFlags().String("timeout", "5m", "Request timeout (30s, 5m, 1h, etc.)")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug output")

	_ = viper.BindPFlag("server", rootCmd.PersistentFlags().Lookup("server"))
	_ = viper.BindPFlag("timeout", rootCmd.PersistentFlags().Lookup("timeout"))
	_ = viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))

	rootCmd.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newTrustCmd(),
		newImageCmd(),
		newArtifactCmd(),
		newVersionCmd(version),
	)
	return rootCmd
}

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("dfcli version %s\n", version)
		},
	}
}
