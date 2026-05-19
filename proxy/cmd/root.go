package cmd

import "github.com/spf13/cobra"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "proxy",
		Short: "Apprentice Proxy: routes Hermes requests to a trained specialist when an input matches a registered pattern.",
		Long: `The Apprentice Proxy sits between Hermes and its upstream model endpoint
(OpenRouter by default).  For each /v1/chat/completions request it embeds the
last user message with BGE-small, cosine-matches against patterns registered via
POST /patterns, and routes matches to a local vLLM specialist; everything else
passes through to the upstream.  Failures from the specialist fall back to the
upstream, and a 5%% sample of matched requests is shadow-sent to the upstream
for offline comparison.

Hermes profile:
  Point Hermes' model_url at this proxy (default http://localhost:8083/v1).
  The proxy speaks the OpenAI chat-completions schema, so no other changes are
  needed.  Patterns are registered via POST /patterns by the detector after
  operator approval.`,
		SilenceUsage: true,
	}
	root.AddCommand(serveCmd())
	root.AddCommand(versionCmd())
	return root
}
