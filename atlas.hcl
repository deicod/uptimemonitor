// atlas.hcl — Atlas configuration for uptimemonitor (SPEC §13).
//
// The atlas CLI is a dev-time tool only. It is used for:
//   - atlas migrate diff  — generate versioned migration files
//   - atlas migrate lint  — check migration quality
//
// The service itself embeds migration files and applies them in-process
// at startup; there is no runtime dependency on the atlas binary.

variable "dev_url" {
  type    = string
  default = "sqlite://file?mode=memory"
}

env "local" {
  src = "file://internal/store/sqlite/schema.sql"
  dev = var.dev_url

  migration {
    dir = "file://internal/store/sqlite/migrations"
  }
}
