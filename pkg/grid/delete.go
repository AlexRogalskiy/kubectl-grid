package grid

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/pkg/errors"
	"github.com/replicatedhq/kubectl-grid/pkg/grid/types"
	"github.com/replicatedhq/kubectl-grid/pkg/logger"
)

func Delete(configFilePath string, g *types.Grid) error {
	gridConfigs, err := List(configFilePath)
	if err != nil {
		return err
	}

	wg := sync.WaitGroup{}
	for _, gridConfig := range gridConfigs {
		for _, clusterConfig := range gridConfig.ClusterConfigs {
			for _, cluster := range g.Spec.Clusters {
				if cluster.EKS == nil && cluster.EKS.NewCluster == nil {
					continue
				}

				if clusterConfig.Name != cluster.EKS.NewCluster.GetDeterministicClusterName() {
					continue
				}

				wg.Add(1)
				go func(config *types.ClusterConfig, cluster *types.ClusterSpec) {
					defer wg.Done()
					err := deleteCluster(config, cluster)
					if err != nil {
						fmt.Printf("cluster %s delete failed with error: %v\n", config.Name, err)
					}
				}(clusterConfig, cluster)
			}
		}
	}

	wg.Wait()

	if err := removeGridFromConfig(g.Name, configFilePath); err != nil {
		return errors.Wrap(err, "failed to remove grid from config")
	}

	return nil
}

func deleteCluster(c *types.ClusterConfig, cluster *types.ClusterSpec) error {
	if c.Provider == "aws" {
		return deleteNewEKSCluster(c, cluster.EKS)
	}

	return nil
}

func deleteNewEKSCluster(c *types.ClusterConfig, cluster *types.EKSSpec) error {
	clusterName := c.GetDeterministicClusterName()

	log := logger.NewLogger()
	log.Info("Deleting EKS cluster %s", clusterName)

	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(c.Region))
	if err != nil {
		return errors.Wrap(err, "failed to load aws config")
	}

	accessKeyID, err := cluster.NewCluster.AccessKeyID.String()
	if err != nil {
		return errors.Wrap(err, "failed to get access key id")
	}
	secretAccessKey, err := cluster.NewCluster.SecretAccessKey.String()
	if err != nil {
		return errors.Wrap(err, "failed to get secret access key")
	}

	cfg.Credentials = credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")

	log.Info("Deleting node group for EKS cluster (this may take a few minutes)")
	err = deleteEKSNodeGroup(cfg, clusterName, clusterName)
	if err != nil {
		return errors.Wrap(err, "failed to delete node group")
	}

	err = waitEKSNodeGroupGone(cfg, clusterName, clusterName)
	if err != nil {
		return errors.Wrap(err, "failed to wait for node group delete")
	}

	log.Info("Deleting EKS cluster")
	err = deleteEKSCluster(cfg, clusterName)
	if err != nil {
		return errors.Wrap(err, "failed to delete cluster")
	}

	return nil
}
