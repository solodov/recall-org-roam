package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"org-search/internal/app"

	"github.com/spf13/cobra"
)

// Run executes the org-search Cobra command tree and renders command results as JSON.
func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, service app.Service) int {
	if service == nil {
		service = app.NewService()
	}

	command := newRootCommand(stdout, service)
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetArgs(args)
	if err := command.ExecuteContext(ctx); err != nil {
		writeJSONError(stderr, err)
		return 1
	}
	return 0
}

func newRootCommand(stdout io.Writer, service app.Service) *cobra.Command {
	var configPath string

	command := &cobra.Command{
		Use:           "org-search",
		Short:         "Index and search Org entries by ID",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.PersistentFlags().StringVar(&configPath, "config", "", "Path to the config txtpb file. Empty uses the default XDG config path.")
	command.AddCommand(
		newRebuildCommand(stdout, service, &configPath),
		newUpdateFileCommand(stdout, service, &configPath),
		newSearchCommand(stdout, service, &configPath),
	)
	return command
}

func newRebuildCommand(stdout io.Writer, service app.Service, configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild the full Org search index",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			result, err := service.Rebuild(command.Context(), app.RebuildRequest{ConfigPath: *configPath})
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		},
	}
}

func newUpdateFileCommand(stdout io.Writer, service app.Service, configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "update-file PATH",
		Short: "Replace one indexed Org file by canonical path",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			result, err := service.UpdateFile(command.Context(), app.UpdateFileRequest{ConfigPath: *configPath, Path: args[0]})
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		},
	}
}

func newSearchCommand(stdout io.Writer, service app.Service, configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "search QUERY",
		Short: "Run one Bleve query against indexed Org entries",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			result, err := service.Search(command.Context(), app.SearchRequest{ConfigPath: *configPath, Query: strings.Join(args, " ")})
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		},
	}
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func writeJSONError(writer io.Writer, err error) {
	_ = writeJSON(writer, map[string]string{"error": err.Error()})
}
