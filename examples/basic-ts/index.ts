import * as pulumi from "@pulumi/pulumi";
import * as filescom from "pulumi-filescom";

const config = new pulumi.Config();
const folderPath = config.require("folderPath");
const groupName = config.require("groupName");
const behaviorName = config.require("behaviorName");

const group = new filescom.Group("group", {
    name: groupName,
    notes: "The group of the basic example.",
});

const behavior = new filescom.Behavior("behavior", {
    behavior: "webhook",
    path: folderPath,
    name: behaviorName,
    value: {
        urls: ["https://example.com/pulumi-filescom-behavior-probe"],
        method: "POST",
        triggers: ["create"],
        headers: { "x-pulumi-filescom-probe": "v1" },
    },
});

export const groupId = group.id;
export const behaviorId = behavior.id;
