package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/ocr"
)

// ErrOCRCommand identifies OCR startup, argument and output failures.
var ErrOCRCommand = errors.New("OCR command failed")

var ocrCmd = &cobra.Command{
	Use: "ocr SOURCE [SOURCE...]", Short: "Recognize text in local images or HTTP(S) URLs (JSON Lines output)",
	Args: cobra.MinimumNArgs(1), RunE: runOCR,
}

func init() { rootCmd.AddCommand(ocrCmd) }

func runOCR(cmd *cobra.Command, args []string) (returnErr error) {
	service, err := ocr.NewContext(cmd.Context(), ocr.Configuration(viper.GetViper()))
	if err != nil {
		return errors.Join(ErrOCRCommand, err)
	}
	defer func() { returnErr = errors.Join(returnErr, service.Close()) }()
	return writeSourceResults(cmd, args, ErrOCRCommand, service.Recognize)
}
