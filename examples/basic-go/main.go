package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/jschady/pulumi-filescom/sdk/go/filescom"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		folderPath := cfg.Require("folderPath")

		group, err := filescom.NewGroup(ctx, "group", &filescom.GroupArgs{
			Name:  pulumi.String(cfg.Require("groupName")),
			Notes: pulumi.String("The group of the basic example."),
		})
		if err != nil {
			return err
		}

		behavior, err := filescom.NewBehavior(ctx, "behavior", &filescom.BehaviorArgs{
			Behavior: pulumi.String("webhook"),
			Path:     pulumi.String(folderPath),
			Name:     pulumi.String(cfg.Require("behaviorName")),
			Value: pulumi.Any(map[string]any{
				"urls":     []string{"https://example.com/pulumi-filescom-behavior-probe"},
				"method":   "POST",
				"triggers": []string{"create"},
				"headers":  map[string]string{"x-pulumi-filescom-probe": "v1"},
			}),
		})
		if err != nil {
			return err
		}

		ctx.Export("groupId", group.ID())
		ctx.Export("behaviorId", behavior.ID())
		return nil
	})
}
