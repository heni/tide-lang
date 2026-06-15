// Loads a JSON config from disk into a typed Tide record and reports
// errors with file/path context.
//
// Forces: structs, the `encoding/json` binding, file I/O, layered error
// handling with `try`. JSON-tagged field names are part of the record's
// declared shape — there is no `any`-typed escape (G23) needed at user
// level.

import fmt
import json
import os

type ServerConfig = {
  host: string
  port: int
}

type LogConfig = {
  level: string
  file:  Option<string>
}

type Config = {
  server: ServerConfig
  log:    LogConfig
  tags:   []string
}

func loadConfig(path: string): Result<Config, error> {
  let data = try os.readFile(path)
  let cfg  = try json.parse<Config>(data)
  return Ok(cfg)
}

func main() {
  if os.args.len() != 2 {
    fmt.println("usage: config_loader <path>")
    os.exit(2)
  }
  match loadConfig(os.args[1]) {
    Ok(cfg) => {
      fmt.println("server:", cfg.server.host + ":", cfg.server.port)
      fmt.println("log:",    cfg.log.level)
      match cfg.log.file {
        Some(f) => fmt.println("log file:", f),
        None    => {},
      }
      fmt.println("tags:", cfg.tags.len(), "entries")
    },
    Err(e) => {
      fmt.println("config error:", e)
      os.exit(1)
    },
  }
}
