package main

import (
	"log"
	"os"

	"github.com/fntlnz/falco-inject/pkg/injector"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

func runCmd(mvflags *cmdutil.MatchVersionFlags) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		streams := genericclioptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		}

		f := cmdutil.NewFactory(mvflags)

		clientConfig, err := f.ToRESTConfig()
		if err != nil {
			log.Fatal(err)
		}
		coreClient, err := corev1client.NewForConfig(clientConfig)
		if err != nil {
			log.Fatal(err)
		}

		selector, err := cmd.Flags().GetString("selector")
		if err != nil {
			log.Fatal(err)
		}
		namespace, err := cmd.Flags().GetString("namespace")
		if err != nil {
			log.Fatal(err)
		}
		a := injector.NewInjector(coreClient, clientConfig, streams)
		err = a.Inject(selector, namespace)
		if err != nil {
			log.Fatal(err)
		}
		err = a.Instrument(selector, namespace)
		if err != nil {
			log.Fatal(err)
		}

	}
}
func main() {
	cmds := &cobra.Command{
		Use: "falco-inject",
	}

	configFlags := genericclioptions.NewConfigFlags(false)
	configFlags.AddFlags(cmds.PersistentFlags())

	kubeConfigFlags := genericclioptions.NewConfigFlags(true).WithDeprecatedPasswordFlag()

	matchVersionKubeConfigFlags := cmdutil.NewMatchVersionFlags(kubeConfigFlags)
	matchVersionKubeConfigFlags.AddFlags(cmds.PersistentFlags())
	cmds.PersistentFlags().String("selector", "app=myapp", "a kubernetes label selector to choose the pods to run Falco against")
	cmds.Run = runCmd(matchVersionKubeConfigFlags)
	err := cmds.Execute()
	if err != nil {
		log.Fatal(err)
	}

}
