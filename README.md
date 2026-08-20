# The Files.com provider for Pulumi

This provider creates and manages the objects in a [Files.com](https://www.files.com) account:

- the folders
- the folder settings that Files.com calls behaviors
- the remote servers
- the groups and the users
- the API keys

This repository wraps the
[Files.com provider for Terraform](https://github.com/Files-com/terraform-provider-files). Every
resource and every data source that provider carries is available here, under a Pulumi name.

## The installation

Run the command for the language your program uses.

### Node.js

```bash
npm install pulumi-filescom
```

### Python

```bash
pip install pulumi-filescom
```

### Go

```bash
go get github.com/jschady/pulumi-filescom/sdk/go/filescom
```

### .NET

```bash
dotnet add package Jschady.Filescom
```

## The configuration

The provider authenticates with one Files.com API key. Set it in the environment:

```bash
export FILES_API_KEY=your-api-key
```

Or set it in the stack configuration, where Pulumi encrypts it:

```bash
pulumi config set --secret filescom:apiKey your-api-key
```

The [installation and configuration page](./docs/installation-configuration.md) carries the full
configuration. It also carries one program in each of the four languages.

## The reference

- [The provider overview and the known limitations](./docs/_index.md)
- [The installation and the configuration](./docs/installation-configuration.md)
- [How to build and test this repository](./CONTRIBUTING.md)

## The licenses

[Apache 2.0](./LICENSE) covers the code in this repository. [The MIT license](./LICENSE-upstream)
covers the upstream provider it wraps, and that license stays with the upstream code.

test