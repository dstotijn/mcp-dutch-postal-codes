// Copyright 2025 David Stotijn
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dstotijn/go-mcp"
)

// baseURL is the base URL for Bert Hubert's testing instance of his
// `bagserv` web service.
// See: https://berthub.eu/articles/posts/dutch-postcode-and-building-database/
const baseURL = "https://berthub.eu/pcode"

// Address represents a Dutch address with postal code information.
type Address struct {
	Street        string   `json:"straat"`
	HouseNumber   int      `json:"huisnummer"`
	HouseLetter   string   `json:"huisletter"`
	HouseSuffix   string   `json:"huistoevoeging"`
	City          string   `json:"woonplaats"`
	PostalCode    string   `json:"postcode"`
	Area          int      `json:"oppervlakte"`
	UsagePurposes []string `json:"gebruiksdoelen"`
	BuildYear     int      `json:"bouwjaar"`
	NumberStatus  string   `json:"num_status"`
	Latitude      float64  `json:"lat,omitempty"`
	Longitude     float64  `json:"lon,omitempty"`
	X             string   `json:"x,omitempty"`
	Y             string   `json:"y,omitempty"`
}

var (
	httpAddr string
	useStdio bool
	useSSE   bool
)

func main() {
	flag.StringVar(&httpAddr, "http", ":8080", "Listen address for JSON-RPC over HTTP")
	flag.BoolVar(&useStdio, "stdio", true, "Enable stdio transport")
	flag.BoolVar(&useSSE, "sse", false, "Enable SSE transport")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	transports := []string{}
	opts := []mcp.ServerOption{}

	if useStdio {
		transports = append(transports, "stdio")
		opts = append(opts, mcp.WithStdioTransport())
	}

	var sseURL url.URL
	if useSSE {
		transports = append(transports, "sse")

		host := "localhost"
		hostPart, port, err := net.SplitHostPort(httpAddr)
		if err != nil {
			log.Fatalf("Failed to split host and port: %v", err)
		}

		if hostPart != "" {
			host = hostPart
		}

		sseURL = url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(host, port),
		}

		opts = append(opts, mcp.WithSSETransport(sseURL))
	}

	mcpServer := mcp.NewServer(mcp.ServerConfig{}, opts...)
	registerPostalCodeTools(mcpServer)

	mcpServer.Start(ctx)

	httpServer := &http.Server{
		Addr:    httpAddr,
		Handler: mcpServer,
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}

	if useSSE {
		go func() {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("HTTP server error: %v", err)
			}
		}()
	}

	log.Printf("MCP server started, using transports: %v", transports)
	if useSSE {
		log.Printf("SSE transport endpoint: %v", sseURL.String())
	}

	// Wait for interrupt signal.
	<-ctx.Done()
	// Restore signal, allowing "force quit".
	stop()

	timeout := 5 * time.Second
	cancelContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	log.Printf("Shutting down server (waiting %s)... Press Ctrl+C to force quit.", timeout)

	var wg sync.WaitGroup

	if useSSE {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := httpServer.Shutdown(cancelContext); err != nil && !errors.Is(err, context.DeadlineExceeded) {
				log.Printf("HTTP server shutdown error: %v", err)
			}
		}()
	}

	wg.Wait()
}

// registerPostalCodeTools registers the tools for postal code lookup.
func registerPostalCodeTools(mcpServer *mcp.Server) {
	// Define the arguments for the `lookup_by_postal_code` tool.
	type lookupByPostalCodeArgs struct {
		PostalCode  string `json:"postalCode"`
		HouseNumber string `json:"houseNumber,omitempty"` // `omitempty` will make this prop optional in the JSON Schema.
		HouseLetter string `json:"houseLetter,omitempty"` // `omitempty` will make this prop optional in the JSON Schema.
	}

	// Define the arguments for the `lookup_by_coordinates` tool.
	type lookupByCoordinatesArgs struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}

	mcpServer.RegisterTools(mcp.CreateTool(mcp.ToolDef[lookupByPostalCodeArgs]{
		Name:        "lookup_by_postal_code",
		Description: "Look up Dutch addresses by postal code and optional house number and letter.",
		HandleFunc: func(ctx context.Context, args lookupByPostalCodeArgs) *mcp.CallToolResult {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			addresses, err := lookupByPostalCode(cctx, args.PostalCode, args.HouseNumber, args.HouseLetter)
			if err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.TextContent{
							Text: fmt.Sprintf("Error looking up postal code: %v", err),
						},
					},
					IsError: true,
				}
			}

			if len(addresses) == 0 {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.TextContent{
							Text: "No addresses found for the given postal code.",
						},
					},
				}
			}

			var contents []mcp.Content

			for _, addr := range addresses {
				contents = append(contents, mcp.TextContent{
					Text: formatAddress(addr),
				})
			}

			return &mcp.CallToolResult{
				Content: contents,
			}
		},
	}))

	mcpServer.RegisterTools(mcp.CreateTool(mcp.ToolDef[lookupByCoordinatesArgs]{
		Name:        "lookup_by_coordinates",
		Description: "Look up the nearest Dutch address by WGS84 (GPS) coordinates.",
		HandleFunc: func(ctx context.Context, args lookupByCoordinatesArgs) *mcp.CallToolResult {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			address, err := lookupByCoordinates(cctx, args.Latitude, args.Longitude)
			if err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.TextContent{
							Text: fmt.Sprintf("Error looking up coordinates: %v", err),
						},
					},
					IsError: true,
				}
			}

			if address == nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.TextContent{
							Text: "No address found for the given coordinates.",
						},
					},
				}
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Text: formatAddress(*address),
					},
				},
			}
		},
	}))
}

// lookupByPostalCode looks up addresses by postal code and optional house number and letter.
func lookupByPostalCode(ctx context.Context, postalCode, houseNumber, houseLetter string) ([]Address, error) {
	// Normalize postal code (remove spaces).
	postalCode = strings.ReplaceAll(postalCode, " ", "")

	requestURL := fmt.Sprintf("%v/%v", baseURL, postalCode)
	if houseNumber != "" {
		requestURL += fmt.Sprintf("/%v", houseNumber)
		if houseLetter != "" {
			requestURL += fmt.Sprintf("/%v", houseLetter)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status code %d", resp.StatusCode)
	}

	var addresses []Address
	if err := json.NewDecoder(resp.Body).Decode(&addresses); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	for i := range addresses {
		addresses[i].PostalCode = postalCode
	}

	return addresses, nil
}

// lookupByCoordinates looks up the nearest address by WGS84 (GPS) coordinates.
func lookupByCoordinates(ctx context.Context, latitude, longitude float64) (*Address, error) {
	requestURL := fmt.Sprintf("%v/%v/%v", baseURL, latitude, longitude)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status code %d", resp.StatusCode)
	}

	var addresses []Address
	if err := json.NewDecoder(resp.Body).Decode(&addresses); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	if len(addresses) == 0 {
		return nil, nil
	}

	return &addresses[0], nil
}

// formatAddress formats an address for display.
func formatAddress(addr Address) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("  Street: %v\n", addr.Street))

	// Format house number with letter and suffix
	houseNumberFull := strconv.Itoa(addr.HouseNumber)
	if addr.HouseLetter != "" {
		houseNumberFull += addr.HouseLetter
	}
	if addr.HouseSuffix != "" {
		houseNumberFull += "-" + addr.HouseSuffix
	}
	sb.WriteString(fmt.Sprintf("  House Number: %v\n", houseNumberFull))

	sb.WriteString(fmt.Sprintf("  Postal Code: %v\n", addr.PostalCode))
	sb.WriteString(fmt.Sprintf("  City: %v\n", addr.City))

	if addr.Area != 0 {
		sb.WriteString(fmt.Sprintf("  Area: %v m²\n", addr.Area))
	}

	if len(addr.UsagePurposes) > 0 {
		sb.WriteString(fmt.Sprintf("  Usage Purposes: %v\n", strings.Join(addr.UsagePurposes, ", ")))
	}

	if addr.BuildYear != 0 {
		sb.WriteString(fmt.Sprintf("  Build Year: %v\n", addr.BuildYear))
	}

	if addr.Latitude != 0 && addr.Longitude != 0 {
		sb.WriteString(fmt.Sprintf("  Coordinates (WGS84): %v, %v\n", addr.Latitude, addr.Longitude))
	}

	if addr.X != "" && addr.Y != "" {
		sb.WriteString(fmt.Sprintf("  Coordinates (Dutch Grid): %v, %v\n", addr.X, addr.Y))
	}

	sb.WriteString("\n")

	return sb.String()
}
