# Terraform Provider for Garage

This repository contains a provider for managing [Garage](https://garagehq.deuxfleurs.fr/), a distributed, S3-compatible object store.

With this provider, you can declaratively manage the following resources in Garage:

- **Buckets**
- **Bucket Aliases**
- **Access Keys**

>[!WARNING]
>Requires Garage version 2.0 or later.

## Getting Started

To get started, refer to the documentation [included in this repository](docs/index.md). It contains a list of options for the provider.

## Local deployment

This repository also provides a script that is ready to use to set up a local instance of Garage.

```sh
cd docker
sh setup.sh
```

## License

This project uses a **dual-license** structure:

- **Source Code:** Licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](./LICENSE-code).  
  You may use, modify, and distribute the code under the terms of the AGPL-3.0.

- **Documentation:** Licensed under the [MIT License](./LICENSE).  
  You are free to use, copy, modify, and distribute the documentation with attribution.

## Disclaimer

I do not have any prior experience with the Go programming language. This provider has been rewritten for Garage v2 with the assistance of LLMs, and is intended for experimental or personal use only.

I make no guarantees regarding the correctness, reliability, or safety of this code. **This software is not intended for use in production environments.** I do not assume any responsibility or liability for issues arising from its use.

Use at your own risk!
