using WebScreenCapture.Client;
using Xunit;

namespace WebScreenCapture.Client.Tests;

public sealed class AnnexBAccessUnitReaderTests
{
    [Fact]
    public void SplitsNvencAnnexBStreamAtAudNalUnitsAcrossChunks()
    {
        var first = new byte[]
        {
            0, 0, 0, 1, 9, 0xf0,
            0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x33,
            0, 0, 1, 0x65, 1, 2, 3,
        };
        var second = new byte[]
        {
            0, 0, 1, 9, 0xf0,
            0, 0, 1, 0x41, 4, 5, 6,
        };
        var third = new byte[]
        {
            0, 0, 0, 1, 9, 0xf0,
            0, 0, 1, 0x41, 7, 8,
        };
        var stream = first.Concat(second).Concat(third).ToArray();
        var reader = new AnnexBAccessUnitReader();
        var frames = new List<byte[]>();

        foreach (var value in stream) frames.AddRange(reader.Push(new[] { value }));
        var final = reader.Flush();
        if (final is not null) frames.Add(final);

        Assert.Equal(3, frames.Count);
        Assert.Equal(first, frames[0]);
        Assert.Equal(second, frames[1]);
        Assert.Equal(third, frames[2]);
    }
}
