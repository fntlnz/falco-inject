package injector

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	tcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/cmd/exec"
	"k8s.io/kubectl/pkg/scheme"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	restclient "k8s.io/client-go/rest"
)

var (
	errFileCannotBeEmpty = errors.New("filepath can not be empty")
)

// Injector ...
type Injector struct {
	genericclioptions.IOStreams
	ctx          context.Context
	CoreV1Client tcorev1.CoreV1Interface
	Config       *restclient.Config
}

// NewInjector ...
func NewInjector(client tcorev1.CoreV1Interface, config *restclient.Config, streams genericclioptions.IOStreams) *Injector {
	return &Injector{
		CoreV1Client: client,
		Config:       config,
		ctx:          context.TODO(),
		IOStreams:    streams,
	}
}

const (
	podNotFoundError              = "no pod found to attach with the given selector"
	podPhaseNotAcceptedError      = "cannot attach into a container in a completed pod; current phase is %s"
	invalidPodContainersSizeError = "unexpected number of containers in digjob pod"
)

// WithContext ...
func (a *Injector) WithContext(c context.Context) {
	a.ctx = c
}

// Inject ...
func (a *Injector) Inject(selector, namespace string) error {
	pl, err := a.CoreV1Client.Pods(namespace).List(metav1.ListOptions{
		LabelSelector: selector,
	})

	if err != nil {
		return err
	}

	if len(pl.Items) == 0 {
		return fmt.Errorf(podNotFoundError)
	}
	pods := pl.Items
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			log.Printf(podPhaseNotAcceptedError, pod.Status.Phase)
			continue
		}

		o := NewCopyOptions(a.IOStreams)
		o.CoreV1Client = a.CoreV1Client
		o.ClientConfig = a.Config
		err := o.copyToPod(
			fileSpec{File: "rootfs.tar"},
			fileSpec{
				PodNamespace: pod.Namespace,
				PodName:      pod.Name,
				File:         "/tmp/rootfs.tar",
			},
			&exec.ExecOptions{},
		)
		if err != nil {
			return err
		}
	}

	return nil

}

type fileSpec struct {
	PodNamespace string
	PodName      string
	File         string
}

// CopyOptions have the data required to perform the copy operation
type CopyOptions struct {
	Container  string
	Namespace  string
	NoPreserve bool

	ClientConfig      *restclient.Config
	CoreV1Client      tcorev1.CoreV1Interface
	ExecParentCmdName string

	genericclioptions.IOStreams
}

// NewCopyOptions creates the options for copy
func NewCopyOptions(ioStreams genericclioptions.IOStreams) *CopyOptions {
	return &CopyOptions{
		IOStreams: ioStreams,
	}
}

func (o *CopyOptions) copyToPod(src, dest fileSpec, options *exec.ExecOptions) error {
	if len(src.File) == 0 || len(dest.File) == 0 {
		return errFileCannotBeEmpty
	}
	reader, writer := io.Pipe()

	// strip trailing slash (if any)
	if dest.File != "/" && strings.HasSuffix(string(dest.File[len(dest.File)-1]), "/") {
		dest.File = dest.File[:len(dest.File)-1]
	}

	if err := o.checkDestinationIsDir(dest); err == nil {
		// If no error, dest.File was found to be a directory.
		// Copy specified src into it
		dest.File = dest.File + "/" + path.Base(src.File)
	}

	go func() {
		defer writer.Close()
		err := makeTar(src.File, dest.File, writer)
		if err != nil {
			log.Printf("error doing makeTar: %v", err)
			return
		}
	}()
	var cmdArr []string

	if o.NoPreserve {
		cmdArr = []string{"tar", "--no-same-permissions", "--no-same-owner", "-xmf", "-"}
	} else {
		cmdArr = []string{"tar", "-xmf", "-"}
	}
	destDir := path.Dir(dest.File)
	if len(destDir) > 0 {
		cmdArr = append(cmdArr, "-C", destDir)
	}

	options.StreamOptions = exec.StreamOptions{
		IOStreams: genericclioptions.IOStreams{
			In:     reader,
			Out:    o.Out,
			ErrOut: o.ErrOut,
		},
		Stdin: true,

		Namespace: dest.PodNamespace,
		PodName:   dest.PodName,
	}

	options.Command = cmdArr
	options.Executor = &exec.DefaultRemoteExecutor{}
	return o.execute(options)
}

// checkDestinationIsDir receives a destination fileSpec and
// determines if the provided destination path exists on the
// pod. If the destination path does not exist or is _not_ a
// directory, an error is returned with the exit code received.
func (o *CopyOptions) checkDestinationIsDir(dest fileSpec) error {
	options := &exec.ExecOptions{
		StreamOptions: exec.StreamOptions{
			IOStreams: genericclioptions.IOStreams{
				Out:    bytes.NewBuffer([]byte{}),
				ErrOut: bytes.NewBuffer([]byte{}),
			},

			Namespace: dest.PodNamespace,
			PodName:   dest.PodName,
		},

		Command:  []string{"test", "-d", dest.File},
		Executor: &exec.DefaultRemoteExecutor{},
	}

	return o.execute(options)
}

func (o *CopyOptions) execute(options *exec.ExecOptions) error {
	if len(options.Namespace) == 0 {
		options.Namespace = o.Namespace
	}

	if len(o.Container) > 0 {
		options.ContainerName = o.Container
	}

	options.Config = o.ClientConfig
	options.PodClient = o.CoreV1Client

	if err := options.Validate(); err != nil {
		return err
	}

	if err := options.Run(); err != nil {
		return err
	}
	return nil
}

func makeTar(srcPath, destPath string, writer io.Writer) error {
	// TODO: use compression here?
	tarWriter := tar.NewWriter(writer)
	defer tarWriter.Close()

	srcPath = path.Clean(srcPath)
	destPath = path.Clean(destPath)
	return recursiveTar(path.Dir(srcPath), path.Base(srcPath), path.Dir(destPath), path.Base(destPath), tarWriter)
}

func recursiveTar(srcBase, srcFile, destBase, destFile string, tw *tar.Writer) error {
	srcPath := path.Join(srcBase, srcFile)
	matchedPaths, err := filepath.Glob(srcPath)
	if err != nil {
		return err
	}
	for _, fpath := range matchedPaths {
		stat, err := os.Lstat(fpath)
		if err != nil {
			return err
		}
		if stat.IsDir() {
			files, err := ioutil.ReadDir(fpath)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				//case empty directory
				hdr, _ := tar.FileInfoHeader(stat, fpath)
				hdr.Name = destFile
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
			}
			for _, f := range files {
				if err := recursiveTar(srcBase, path.Join(srcFile, f.Name()), destBase, path.Join(destFile, f.Name()), tw); err != nil {
					return err
				}
			}
			return nil
		} else if stat.Mode()&os.ModeSymlink != 0 {
			//case soft link
			hdr, _ := tar.FileInfoHeader(stat, fpath)
			target, err := os.Readlink(fpath)
			if err != nil {
				return err
			}

			hdr.Linkname = target
			hdr.Name = destFile
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
		} else {
			//case regular file or other file type like pipe
			hdr, err := tar.FileInfoHeader(stat, fpath)
			if err != nil {
				return err
			}
			hdr.Name = destFile

			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}

			f, err := os.Open(fpath)
			if err != nil {
				return err
			}
			defer f.Close()

			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
			return f.Close()
		}
	}
	return nil
}

func (a *Injector) Instrument(selector, namespace string) error {

	// This should in reality do a `pdig -p 1` but it's
	// not working as intended right now so I'm telling
	// pdig to execute some "malicious" workload instead
	command := `
cd /tmp
tar -xvf rootfs.tar
cd rootfs
cp -r -n * /
/pdig -a "sh -c 'while true; do touch /tmp/test && chmod +s /tmp/test; do done'"
falco -u
	`
	cmd := []string{
		"sh",
		"-c",
		command,
	}
	pl, err := a.CoreV1Client.Pods(namespace).List(metav1.ListOptions{
		LabelSelector: selector,
	})

	if err != nil {
		return err
	}

	if len(pl.Items) == 0 {
		return fmt.Errorf(podNotFoundError)
	}
	pods := pl.Items
	for _, pod := range pods {
		req := a.CoreV1Client.RESTClient().Post().Resource("pods").Name(pod.Name).
			Namespace("default").SubResource("exec")
		option := &v1.PodExecOptions{
			Command: cmd,
			Stdin:   true,
			Stdout:  true,
			Stderr:  true,
			TTY:     true,
		}

		req.VersionedParams(
			option,
			scheme.ParameterCodec,
		)
		exec, err := remotecommand.NewSPDYExecutor(a.Config, "POST", req.URL())
		if err != nil {
			return err
		}
		err = exec.Stream(remotecommand.StreamOptions{
			Stdin:  a.IOStreams.In,
			Stdout: a.IOStreams.Out,
			Stderr: a.IOStreams.ErrOut,
		})
		if err != nil {
			return err
		}
	}

	return nil
}
