import pulumi
import pulumi_filescom as filescom

config = pulumi.Config()
folder_path = config.require("folderPath")
group_name = config.require("groupName")
behavior_name = config.require("behaviorName")

group = filescom.Group(
    "group",
    name=group_name,
    notes="The group of the basic example.",
)

behavior = filescom.Behavior(
    "behavior",
    behavior="webhook",
    path=folder_path,
    name=behavior_name,
    value={
        "urls": ["https://example.com/pulumi-filescom-behavior-probe"],
        "method": "POST",
        "triggers": ["create"],
        "headers": {"x-pulumi-filescom-probe": "v1"},
    },
)

pulumi.export("groupId", group.id)
pulumi.export("behaviorId", behavior.id)
