# controller-runtime-example
An example project that is multi-cluster aware and works with [kcp](https://github.com/kcp-dev/kcp)

## Description
This repository contains an example project that works with APIExports and multiple kcp workspaces. It demonstrates
two reconcilers:

1. ConfigMap
   1. Get a ConfigMap for the key from the queue, from the correct logical cluster
   2. If the ConfigMap has labels["name"], set labels["response"] = "hello-$name" and save the changes
   3. List all ConfigMaps in the logical cluster and log each one's namespace and name
   4. If the ConfigMap from step 1 has data["namespace"] set, create a namespace whose name is the data value.
   5. If the ConfigMap from step 1 has data["secretData"] set, create a secret in the same namespace as the ConfigMap,
      with an owner reference to the ConfigMap, and data["dataFromCM"] set to the data value.

2. Widget
   1. Show how to list all Widget instances across all logical clusters
   2. Get a Widget for the key from the queue, from the correct logical cluster
   3. List all Widgets in the same logical cluster
   4. Count the number of Widgets (list length)
   5. Make sure `.status.total` matches the current count (via a `patch`)

## Getting Started

### Running on kcp

1. Build and push your image to the location specified by `IMG`:
	
```sh
make docker-build docker-push REGISTRY=<some-registry> IMG=controller-runtime-example:tag
```
	
1. Deploy the controller to kcp with the image specified by `IMG`:

```sh
make deploy REGISTRY=<some-registry> IMG=controller-runtime-example:tag
```

### Uninstall resources
To delete the resources from kcp:

```sh
make uninstall
```

### Undeploy controller
Undeploy the controller from kcp:

```sh
make undeploy
```

## Contributing
See [CONTRIBUTING.md](CONTRIBUTING.md)

### How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/) 
which provides a reconcile function responsible for synchronizing resources until the desired state is reached. 

### Test It Out
1. Install the required resources into kcp:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions
If you are editing the API definitions, regenerate the manifests using:

```sh
make manifests apiresourceschemas
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

