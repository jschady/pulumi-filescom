using System.Collections.Generic;
using Pulumi;
using Filescom = Jschady.Filescom;

return await Deployment.RunAsync(() =>
{
    var config = new Config();
    var folderPath = config.Require("folderPath");

    var group = new Filescom.Group("group", new Filescom.GroupArgs
    {
        Name = config.Require("groupName"),
        Notes = "The group of the basic example.",
    });

    var behavior = new Filescom.Behavior("behavior", new Filescom.BehaviorArgs
    {
        BehaviorType = "webhook",
        Path = folderPath,
        Name = config.Require("behaviorName"),
        Value = new Dictionary<string, object>
        {
            ["urls"] = new[] { "https://example.com/pulumi-filescom-behavior-probe" },
            ["method"] = "POST",
            ["triggers"] = new[] { "create" },
            ["headers"] = new Dictionary<string, string> { ["x-pulumi-filescom-probe"] = "v1" },
        },
    });

    return new Dictionary<string, object?>
    {
        ["groupId"] = group.Id,
        ["behaviorId"] = behavior.Id,
    };
});
