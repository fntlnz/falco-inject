# Falco Inject

> Inject Falco and pdig into a running kubernetes pod.


This repo exists to show the concept that Falco + pdig can be
injected into running pods without requiring to modify the original image.

**Warning**: I'm doing expriments here, don't take this seriously.

[![asciicast](https://asciinema.org/a/83utf2y7g43bGtKEc7ccxLGiA.svg)](https://asciinema.org/a/83utf2y7g43bGtKEc7ccxLGiA)


In order to inject, you need a rootfs containing Falco, its dependencies and pdig.
Since this is a proof of concept now, I created one from scratch and uploaded
it to one of my buckets. It basically contains the Falco pieces bundled in
the docker image  `krisnova/falco-trace:latest`.
In case you want to create your own rootfs tar, in order for this to work
you need the `falco` binary and the `pdig` binary.


So you need to download the file `rootfs.tar` and then run `falco-inject`
that will inject the content of `rootfs.tar` file from the current working directory
into the specified pod with a label selector.

```
curl -O https://fs.fntlnz.wtf/falco/rootfs.tar
./falco-inject --selector "run=unsecure-falco-example" --namespace default
```



## Full example (I just want to try this!)

```
kubectl run unsecure-falco-example --image debian -- sleep 9999999
curl -O https://fs.fntlnz.wtf/falco/rootfs.tar
go build .
./falco-inject --selector "run=unsecure-falco-example" --namespace default
```

## FAQ

Q. How does this connect to kubernetes?

A. It uses your main kubeconfig file, if you expose the `KUBECONFIG` environment variable, it uses that one

Q. Can I use this in production?

A. Remember that discussion we had about trusting my 1-day-old repositories? USE IT

## Known issues

- Looks like the attach feature of pdig is not working properly, see `injector.go` for details.
- Looks like Falco can't read from process directories not owned by the root user while running in an unprivileged pod
