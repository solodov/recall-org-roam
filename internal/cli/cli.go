package cli

import (
	"context"
	"io"

	"org-recall-index/internal/app"

	recallprovider "github.com/solodov/recall/provider"
	"github.com/spf13/cobra"
)

// Run executes the org-recall-index Cobra command tree and renders command results in human-readable or JSON form.
func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, service app.Service) int {
	return RunWithIO(ctx, args, nil, stdout, stderr, service)
}

// RunWithIO executes the command tree with explicit streams for stdio provider integrations.
func RunWithIO(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, service app.Service) int {
	if service == nil {
		service = app.NewService()
	}

	options := renderOptions{}
	command := newRootCommand(stdin, stdout, &options, service)
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetArgs(args)
	if err := command.ExecuteContext(ctx); err != nil {
		writeError(stderr, err, options.jsonOutput)
		return 1
	}
	return 0
}

func newRootCommand(stdin io.Reader, stdout io.Writer, options *renderOptions, service app.Service) *cobra.Command {
	var configPath string

	command := &cobra.Command{
		Use:           "org-recall-index",
		Short:         "Index Org entries for recall",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.PersistentFlags().StringVar(&configPath, "config", "", "Path to the config txtpb file. Empty uses the default XDG config path.")
	command.PersistentFlags().BoolVar(&options.jsonOutput, "json", false, "Render command output as JSON for automation.")
	command.AddCommand(
		newRebuildCommand(stdout, options, service, &configPath),
		newUpdateFileCommand(stdout, options, service, &configPath),
		newRecallProviderCommand(stdin, stdout, service, &configPath),
	)
	return command
}

func newRebuildCommand(stdout io.Writer, options *renderOptions, service app.Service, configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild the full Org search index",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			result, err := service.Rebuild(command.Context(), app.RebuildRequest{ConfigPath: *configPath})
			if err != nil {
				return err
			}
			return writeResult(stdout, result, options.jsonOutput)
		},
	}
}

func newUpdateFileCommand(stdout io.Writer, options *renderOptions, service app.Service, configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "update-file PATH",
		Short: "Replace one indexed Org file by canonical path",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			result, err := service.UpdateFile(command.Context(), app.UpdateFileRequest{ConfigPath: *configPath, Path: args[0]})
			if err != nil {
				return err
			}
			return writeResult(stdout, result, options.jsonOutput)
		},
	}
}

func newRecallProviderCommand(stdin io.Reader, stdout io.Writer, service app.Service, configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:    "recall-provider RPC_PATH",
		Short:  "Serve one recall SearchProvider RPC over stdio",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return recallprovider.ServeSearchWithOptions(command.Context(), app.NewRecallProvider(service, *configPath), recallprovider.ServeOptions{
				Stdin:  stdin,
				Stdout: stdout,
				Args:   args,
			})
		},
	}
}

type renderOptions struct {
	jsonOutput bool
}
