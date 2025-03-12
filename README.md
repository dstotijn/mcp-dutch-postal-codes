# mcp-dutch-postal-codes

[MCP](https://modelcontextprotocol.io/) server for retrieving Dutch address information by postcal code or WGS84
coordinates.

## Requirements

- [Go 1.24](https://go.dev/dl/)

## Installation

Configuration for common MCP hosts (Claude Desktop, Cursor):

```json
{
  "mcpServers": {
    "dutch-postal-codes": {
      "command": "go",
      "args": ["run", "github.com/dstotijn/mcp-dutch-postal-codes@main"]
    }
  }
}
```

Alternatively, you can manually install the program (given you have Go installed):

```sh
go install github.com/dstotijn/mcp-dutch-postal-codes@main
```

## Usage

```
$ mcp-dutch-postal-codes --help

Usage of mcp-dutch-postal-codes:
  -http string
        HTTP listen address for JSON-RPC over HTTP (default ":8080")
  -sse
        Enable SSE transport
  -stdio
        Enable stdio transport (default true)
```

Typically, your MCP host will run the program and start the MCP server, and you
don't need to manually do this. But if you want to run the MCP server manually,
for instance because you want to serve over HTTP (using SSE):

Given you have your `PATH` environment configured to include the path named by
`$GOBIN` (or `$GOPATH/bin` `$HOME/go/bin` if `$GOBIN` is not set), you can then
run:

```sh
mcp-dutch-postal-codes --stdio=false --sse
```

Which will output the SSE transport URL:

```
2025/03/12 15:20:01 MCP server started, using transports: [sse]
2025/03/12 15:20:01 SSE transport endpoint: http://localhost:8080
```

## Acknowledgements

Uses Bert Hubert's testing instance of his `bagserv` web service
([Article](https://berthub.eu/articles/posts/dutch-postcode-and-building-database/)).

## License

[Apache-2.0 license](/LICENSE)

---

©️ 2025 David Stotijn
