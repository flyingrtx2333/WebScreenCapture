using System.IO;

namespace WebScreenCapture.Client;

/// <summary>
/// Splits an Annex-B H.264 byte stream into access units. The NVENC process is
/// configured to insert an AUD NAL before every frame, which makes the framing
/// deterministic without parsing slice headers.
/// </summary>
public sealed class AnnexBAccessUnitReader
{
    private const int MaxBufferedBytes = 32 * 1024 * 1024;
    private readonly List<byte> _buffer = [];
    private int _scanOffset;
    private int _audOffset = -1;

    public IReadOnlyList<byte[]> Push(ReadOnlySpan<byte> bytes)
    {
        if (!bytes.IsEmpty)
        {
            for (var index = 0; index < bytes.Length; index++) _buffer.Add(bytes[index]);
        }
        if (_buffer.Count > MaxBufferedBytes)
        {
            throw new InvalidDataException("H.264 access unit exceeded the framing buffer limit.");
        }

        var frames = new List<byte[]>();
        while (TryFindNal(_scanOffset, out var startOffset, out var headerOffset))
        {
            if (headerOffset >= _buffer.Count)
            {
                _scanOffset = startOffset;
                break;
            }

            var nalType = _buffer[headerOffset] & 0x1f;
            if (nalType == 9)
            {
                if (_audOffset >= 0 && startOffset > _audOffset)
                {
                    frames.Add(_buffer.GetRange(0, startOffset).ToArray());
                    var nextScanOffset = headerOffset - startOffset + 1;
                    _buffer.RemoveRange(0, startOffset);
                    _audOffset = 0;
                    _scanOffset = nextScanOffset;
                    continue;
                }
                _audOffset = startOffset;
            }
            _scanOffset = headerOffset + 1;
        }
        return frames;
    }

    public byte[]? Flush()
    {
        if (_audOffset < 0 || _buffer.Count == 0) return null;
        var frame = _buffer.ToArray();
        Reset();
        return frame;
    }

    public void Reset()
    {
        _buffer.Clear();
        _scanOffset = 0;
        _audOffset = -1;
    }

    private bool TryFindNal(int offset, out int startOffset, out int headerOffset)
    {
        for (var index = Math.Max(0, offset); index + 3 <= _buffer.Count; index++)
        {
            if (_buffer[index] != 0 || _buffer[index + 1] != 0) continue;
            if (index + 3 < _buffer.Count && _buffer[index + 2] == 0 && _buffer[index + 3] == 1)
            {
                startOffset = index;
                headerOffset = index + 4;
                return true;
            }
            if (_buffer[index + 2] == 1)
            {
                startOffset = index;
                headerOffset = index + 3;
                return true;
            }
        }
        startOffset = -1;
        headerOffset = -1;
        return false;
    }
}
