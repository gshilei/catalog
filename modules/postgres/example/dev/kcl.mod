[package]
name = "example"

[dependencies]
kam = { git = "https://github.com/KusionStack/kam.git", tag = "0.2.0" }
service = {oci = "oci://ghcr.io/kusionstack/service", tag = "0.1.0" }
network = { oci = "oci://ghcr.io/kusionstack/network", tag = "0.2.0" }
postgres = { oci = "oci://ghcr.io/kusionstack/postgres", tag = "0.2.0" }

[profile]
entries = ["main.k"]