/*
Copyright 2023 The K8sGPT Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package serve

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/k8sgpt-ai/k8sgpt/pkg/ai"
	"github.com/k8sgpt-ai/k8sgpt/pkg/kubernetes"
	k8sgptserver "github.com/k8sgpt-ai/k8sgpt/pkg/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

const (
	defaultTemperature float32 = 0.7
	defaultTopP        float32 = 1.0
	defaultTopK        int32   = 50
	defaultMaxTokens   int     = 2048
)

var (
	port        string
	metricsPort string
	backend     string
	enableHttp  bool
	enableMCP   bool
	mcpPort     string
	mcpHTTP     bool
	// filters can be injected into the server (repeatable flag)
	filters []string
	// extraClusters holds additional cluster specs provided via --extra-cluster.
	// Each element uses the format: "id:kubeconfigPath" or "id:kubeconfigPath:kubecontext".
	extraClusters []string
)

// parseExtraCluster parses a single --extra-cluster value.
// Accepted formats:
//
//	"id:kubeconfigPath"              — uses the default context in the kubeconfig
//	"id:kubeconfigPath:kubecontext"  — uses the specified context
//
// Returns (id, kubeconfigPath, kubecontext, error).
func parseExtraCluster(spec string) (id, kubeconfig, kubecontext string, err error) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf(
			"invalid --extra-cluster value %q: expected format 'id:kubeconfigPath' or 'id:kubeconfigPath:kubecontext'",
			spec,
		)
	}
	id = parts[0]
	kubeconfig = parts[1]
	if len(parts) == 3 {
		kubecontext = parts[2]
	}
	return
}

var ServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Runs k8sgpt as a server",
	Long:  `Runs k8sgpt as a server to allow for easy integration with other applications.`,
	Run: func(cmd *cobra.Command, args []string) {

		var configAI ai.AIConfiguration
		err := viper.UnmarshalKey("ai", &configAI)
		if err != nil {
			color.Red("Error: %v", err)
			os.Exit(1)
		}
		var aiProvider *ai.AIProvider
		if len(configAI.Providers) == 0 {
			// we validate and set temperature for our backend
			temperature := func() float32 {
				env := os.Getenv("K8SGPT_TEMPERATURE")
				if env == "" {
					return defaultTemperature
				}
				temperature, err := strconv.ParseFloat(env, 32)
				if err != nil {
					color.Red("Unable to convert Temperature value: %v", err)
					os.Exit(1)
				}
				if temperature > 1.0 || temperature < 0.0 {
					color.Red("Error: temperature ranges from 0 to 1.")
					os.Exit(1)
				}
				return float32(temperature)
			}
			topP := func() float32 {
				env := os.Getenv("K8SGPT_TOP_P")
				if env == "" {
					return defaultTopP
				}
				topP, err := strconv.ParseFloat(env, 32)
				if err != nil {
					color.Red("Unable to convert topP value: %v", err)
					os.Exit(1)
				}
				if topP > 1.0 || topP < 0.0 {
					color.Red("Error: topP ranges from 0 to 1.")
					os.Exit(1)
				}
				return float32(topP)
			}
			topK := func() int32 {
				env := os.Getenv("K8SGPT_TOP_K")
				if env == "" {
					return defaultTopK
				}
				topK, err := strconv.ParseFloat(env, 32)
				if err != nil {
					color.Red("Unable to convert topK value: %v", err)
					os.Exit(1)
				}
				if topK < 10 || topK > 100 {
					color.Red("Error: topK ranges from 1 to 100.")
					os.Exit(1)
				}
				return int32(topK)
			}
			maxTokens := func() int {
				env := os.Getenv("K8SGPT_MAX_TOKENS")
				if env == "" {
					return defaultMaxTokens
				}
				maxTokens, err := strconv.ParseInt(env, 10, 32)
				if err != nil {
					color.Red("Unable to convert maxTokens value: %v", err)
					os.Exit(1)
				}
				return int(maxTokens)
			}

			// Parse custom headers from environment variable
			parseCustomHeaders := func() []http.Header {
				headersEnv := os.Getenv("K8SGPT_CUSTOM_HEADERS")
				if headersEnv == "" {
					return nil
				}

				header := make(http.Header)
				headerPairs := strings.Split(headersEnv, ",")
				for _, pair := range headerPairs {
					kv := strings.SplitN(pair, ":", 2)
					if len(kv) == 2 {
						header.Add(strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1]))
					}
				}
				return []http.Header{header}
			}
			// Check for env injection
			backend = os.Getenv("K8SGPT_BACKEND")
			password := os.Getenv("K8SGPT_PASSWORD")
			model := os.Getenv("K8SGPT_MODEL")
			baseURL := os.Getenv("K8SGPT_BASEURL")
			engine := os.Getenv("K8SGPT_ENGINE")
			azureAPIType := os.Getenv("K8SGPT_AZURE_API_TYPE")
			proxyEndpoint := os.Getenv("K8SGPT_PROXY_ENDPOINT")
			providerId := os.Getenv("K8SGPT_PROVIDER_ID")
			// If the envs are set, allocate in place to the aiProvider
			// else exit with error
			envIsSet := backend != "" || password != "" || model != ""
			if envIsSet {
				aiProvider = &ai.AIProvider{
					Name:          backend,
					Password:      password,
					Model:         model,
					BaseURL:       baseURL,
					Engine:        engine,
					AzureAPIType:  azureAPIType,
					CustomHeaders: parseCustomHeaders(),
					ProxyEndpoint: proxyEndpoint,
					ProviderId:    providerId,
					Temperature:   temperature(),
					TopP:          topP(),
					TopK:          topK(),
					MaxTokens:     maxTokens(),
				}

				configAI.Providers = append(configAI.Providers, *aiProvider)

				viper.Set("ai", configAI)
				if err := viper.WriteConfig(); err != nil {
					color.Red("Error writing config file: %s", err.Error())
					os.Exit(1)
				}
			} else {
				color.Red("Error: AI provider not specified in configuration. Please run k8sgpt auth")
				os.Exit(1)
			}
		}
		if aiProvider == nil {
			for _, provider := range configAI.Providers {
				if backend == provider.Name {
					// the pointer to the range variable is not really an issue here, as there
					// is a break right after, but to prevent potential future issues, a temp
					// variable is assigned
					p := provider
					aiProvider = &p
					break
				}
			}
		}

		if aiProvider == nil || aiProvider.Name == "" {
			color.Red("Error: AI provider %s not specified in configuration. Please run k8sgpt auth", backend)
			os.Exit(1)
		}

		logger, err := zap.NewProduction()
		if err != nil {
			color.Red("failed to create logger: %v", err)
			os.Exit(1)
		}
		defer func() {
			if err := logger.Sync(); err != nil {
				color.Red("failed to sync logger: %v", err)
				os.Exit(1)
			}
		}()

		if enableMCP {
			// Register extra clusters before starting the MCP server so that all
			// clusters are available the moment the first tool call arrives.
			for _, spec := range extraClusters {
				clusterID, kubecfg, kubectx, parseErr := parseExtraCluster(spec)
				if parseErr != nil {
					color.Red("Error: %v", parseErr)
					os.Exit(1)
				}
				if _, regErr := kubernetes.DefaultManager().RegisterCluster(clusterID, kubecfg, kubectx); regErr != nil {
					color.Red("Error registering extra cluster %q (%s): %v", clusterID, spec, regErr)
					os.Exit(1)
				}
				color.Green("Registered extra cluster: id=%q kubeconfig=%q kubecontext=%q", clusterID, kubecfg, kubectx)
			}

			// Create and start MCP server
			mcpServer, err := k8sgptserver.NewMCPServer(mcpPort, aiProvider, mcpHTTP, logger)
			if err != nil {
				color.Red("Error creating MCP server: %v", err)
				os.Exit(1)
			}
			go func() {
				if err := mcpServer.Start(); err != nil {
					color.Red("Error starting MCP server: %v", err)
					os.Exit(1)
				}
			}()
		}

		// Allow metrics port to be overridden by environment variable
		if envMetricsPort := os.Getenv("K8SGPT_METRICS_PORT"); envMetricsPort != "" && !cmd.Flags().Changed("metrics-port") {
			metricsPort = envMetricsPort
		}

		server := k8sgptserver.Config{
			Backend:     aiProvider.Name,
			Port:        port,
			MetricsPort: metricsPort,
			EnableHttp:  enableHttp,
			Token:       aiProvider.Password,
			Logger:      logger,
			Filters:     filters,
		}
		go func() {
			if err := server.ServeMetrics(); err != nil {
				color.Red("Error: %v", err)
				os.Exit(1)
			}
		}()

		go func() {
			if err := server.Serve(); err != nil {
				color.Red("Error: %v", err)
				os.Exit(1)
			}
		}()

		// Wait for both servers to exit
		select {}
	},
}

func init() {
	// add flag for backend
	ServeCmd.Flags().StringVarP(&port, "port", "p", "8080", "Port to run the server on")
	ServeCmd.Flags().StringVarP(&metricsPort, "metrics-port", "m", "8081", "Port to run the metrics-server on (env: K8SGPT_METRICS_PORT)")
	ServeCmd.Flags().StringVarP(&backend, "backend", "b", "openai", "Backend AI provider")
	ServeCmd.Flags().BoolVarP(&enableHttp, "http", "", false, "Enable REST/http using gppc-gateway")
	ServeCmd.Flags().BoolVarP(&enableMCP, "mcp", "", false, "Enable Mission Control Protocol server")
	ServeCmd.Flags().StringVarP(&mcpPort, "mcp-port", "", "8089", "Port to run the MCP server on")
	ServeCmd.Flags().BoolVarP(&mcpHTTP, "mcp-http", "", false, "Enable HTTP mode for MCP server")
	// allow injecting filters into the running server (repeatable)
	ServeCmd.Flags().StringSliceVar(&filters, "filter", []string{}, "Filter to apply (can be specified multiple times)")
	// allow registering extra clusters for multi-cluster MCP support (repeatable)
	// Format: "id:kubeconfigPath" or "id:kubeconfigPath:kubecontext"
	// Example: --extra-cluster "prod:/home/user/.kube/prod.yaml:prod-ctx"
	ServeCmd.Flags().StringArrayVar(&extraClusters, "extra-cluster", []string{},
		`Register an additional Kubernetes cluster for multi-cluster MCP support.
	Format: "id:kubeconfigPath" or "id:kubeconfigPath:kubecontext".
	Can be specified multiple times.
	Example: --extra-cluster "prod:/home/user/.kube/prod.yaml:prod-ctx"
	         --extra-cluster "staging:/home/user/.kube/staging.yaml"`,
	)
}
