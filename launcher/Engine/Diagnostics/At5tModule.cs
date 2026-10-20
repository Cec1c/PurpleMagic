namespace Dnf90.Launcher.Diagnostics;

// Port of the reviewed ServerS5A3 AT-5T sampler (87861a0), with bounded reads.
// Layout provenance and field coverage: docs/LAUNCHER_DIAGNOSTICS.md.
internal sealed class At5tModule(IDiagnosticMemory memory, uint moduleBase)
{
    public const string ModuleVersion = "at5t-readonly-1";
    public static string VersionFor(string hash, long size) => size != 111812608 ? "unsupported" : hash switch {
        "8de790c1974fe74d3b74ec6095b7281ea337759a4adcdfebdd874bf67a94126f" => "V135",
        "26abf551753c2745c2e5b349fb24336422a189187ff847c995e0df9a68871079" => "V141", _ => "unsupported" };
    private uint Global(uint address) => checked(moduleBase + address - 0x400000);
    private readonly Dictionary<string, DiagnosticField> fields = [];
    private readonly List<(uint Address, byte[] Bytes)> guards = [];
    private uint cachedScene, cachedBegin, cachedEnd;
    private double cachedAt = -10000;
    private readonly List<(uint Slot, uint Object)> vehicles = [];
    public bool HasVehicle { get; private set; }
    public uint Scene { get; private set; }
    public bool Fresh { get; private set; }
    public string Quality { get; private set; } = "ok";
    private void Value(string name, double value) => fields[name] = new(value);
    private void Missing(string name, string status, string reason) => fields[name] = new(null, status, reason);
    private byte[]? Bytes(string name, uint address, int size, bool guard = false)
    {
        try
        {
            if (address < 0x10000 || size > 65536 || (ulong)address + (uint)size > (ulong)uint.MaxValue + 1) throw new IOException();
            var b = memory.Read(address, size); if (b.Length != size) throw new IOException();
            if (guard) guards.Add((address, b)); return b;
        }
        catch (Exception e) when (e is IOException or OverflowException) { Missing(name, "read_failed", "memory_read"); return null; }
    }
    private uint? U32(string name, uint address, bool guard = false)
    { var b = Bytes(name, address, 4, guard); if (b is null) return null; var v = BitConverter.ToUInt32(b); Value(name, v); return v; }
    private void U8(string name, uint address) { var b = Bytes(name, address, 1); if (b is not null) Value(name, b[0]); }
    private void I32(string name, uint address) { var value = U32(name, address); if (value is not null) Value(name, unchecked((int)value.Value)); }
    private byte[]? Vector(string name, uint baseAddress, uint beginOffset, uint endOffset, int stride, int max, out uint begin)
    {
        begin = 0;
        var b = U32(name + ".begin", checked(baseAddress + beginOffset), true); var e = U32(name + ".end", checked(baseAddress + endOffset), true);
        if (b is null || e is null) return null;
        if (e < b || (e - b) % stride != 0 || (e - b) / stride > max) { Missing(name, "read_failed", "vector_bounds"); return null; }
        begin = b.Value; Value(name + ".count", (e.Value - b.Value) / stride);
        return e == b ? [] : Bytes(name, b.Value, (int)(e.Value - b.Value), name == "rules" || name.StartsWith("rule."));
    }
    private void Action(string prefix, uint address)
    {
        var b = Bytes(prefix, address, 8, true); if (b is null) return;
        var handle = BitConverter.ToUInt32(b); var encoded = BitConverter.ToUInt32(b, 4);
        // Keys stay internal; only the decoded state and quality are emitted.
        uint ReadKey(uint p) { var data = memory.Read(p, 4); if (data.Length != 4) throw new IOException(); guards.Add((p, data)); return BitConverter.ToUInt32(data); }
        try
        {
            var manager = ReadKey(Global(0x057F8A18)); if (manager < 0x10000) throw new IOException();
            var bank = ReadKey(checked((manager + 0x24) + (handle >> 16) * 4)); if (bank < 0x10000) throw new IOException();
            var key = ReadKey(checked((bank + 0x2114) + (handle & 0xffff) * 4)); var k = key & 0xffff;
            var plain = encoded ^ (k * 0x10001); var low = plain & 0xffff; var high = plain >> 16;
            uint check = (address & 15) switch { 0 => high - k + low, 4 => low - high, 8 => high * low, 12 => high + low + k, _ => high + low };
            if (((check & 0xffff) ^ k) != key >> 16) Missing(prefix, "read_failed", "protection_integrity");
            else Value(prefix, unchecked((int)plain));
        }
        catch (Exception e) when (e is IOException or OverflowException) { Missing(prefix, "read_failed", "protection_key"); }
    }
    private (double X, double Y)? Coordinates(string prefix, uint obj)
    {
        // 0288FA70 -> physics+4 (or inline +0C) -> verified 0288E950 float getter.
        var physics = U32(prefix + ".physics", checked((obj + 0xb8)), true);
        if (physics is null or 0) { Missing(prefix + ".position", physics == 0 ? "not_applicable" : "read_failed", "no_physics"); return null; }
        var proxy = U32(prefix + ".physics_proxy", checked((physics.Value + 0x94)), true);
        var active = Bytes(prefix + ".physics_proxy_active", checked((physics.Value + 0x98)), 1, true);
        if (proxy is null || active is null) return null;
        if (proxy != 0 && active[0] != 0) { Missing(prefix + ".position", "unsupported", "delegated_physics_getter"); return null; }
        var pointer = U32(prefix + ".position_vector", checked(physics.Value + 4), true);
        if (pointer is null) return null;
        var vector = pointer == 0 ? checked(physics.Value + 12) : pointer.Value;
        var vt = U32(prefix + ".position_vtable", vector, true);
        var getter = vt is > 0 ? U32(prefix + ".position_getter", vt.Value, true) : null;
        if (getter != Global(0x0288e950)) { Missing(prefix + ".position", "unsupported", "unknown_position_getter"); return null; }
        var data = Bytes(prefix + ".position", checked(vector + 4), 12, true);
        if (data is null) return null;
        var xyz = new[] { BitConverter.ToSingle(data), BitConverter.ToSingle(data, 4), BitConverter.ToSingle(data, 8) };
        if (xyz.Any(v => !float.IsFinite(v) || Math.Abs(v) > 10000000)) { Missing(prefix + ".position", "read_failed", "coordinate_bounds"); return null; }
        Value(prefix + ".x", xyz[0]); Value(prefix + ".y", xyz[1]); Value(prefix + ".z", xyz[2]);
        return (xyz[0], xyz[1]);
    }
    public Dictionary<string, DiagnosticField> Sample(double nowMs, bool detailed)
    {
        fields.Clear(); guards.Clear(); Fresh = false; HasVehicle = false; Scene = 0;
        var primary = U32("identity.primary", Global(0x057b6fb0), true);
        var alternate = U32("identity.override", Global(0x057b6fb4), true);
        U32("identity.virtual_character", Global(0x057b6fb8), true);
        // A failed override read must not silently select primary.
        var actor = alternate is null ? null : alternate == 0 ? primary : alternate;
        if (actor is > 0xffff8fff) { Missing("actor", "read_failed", "pointer_bounds"); return Complete(); }
        if (actor is null or 0) { Missing("actor", actor is null ? "read_failed" : "not_applicable", "no_stable_actor"); return Complete(); }
        Value("actor.address", actor.Value);
        var scene = U32("scene.address", checked(actor.Value + 0xc8), true); Scene = scene ?? 0;
        if (scene is > 0)
        {
            var begin = U32("scene.begin", scene.Value + 12, true); var end = U32("scene.end", scene.Value + 16, true);
            var refresh = scene != cachedScene || begin != cachedBegin || end != cachedEnd || nowMs - cachedAt >= 250;
            if (!refresh)
                foreach (var (slot, vehicle) in vehicles)
                    if (U32("scene.cached_slot", slot, true) != vehicle || U32("scene.cached_marker", checked(vehicle + 0x400), true) != 61235) { refresh = true; break; }
            if (refresh)
            {
                vehicles.Clear(); var rows = Vector("scene.objects", scene.Value, 12, 16, 4, 4096, out var start);
                if (rows is not null)
                {
                    for (var i = 0; i < rows.Length && vehicles.Count < 8; i += 4)
                    {
                        var obj = BitConverter.ToUInt32(rows, i); if (obj < 0x10000 || obj > uint.MaxValue - 0x7000) continue;
                        try { if (BitConverter.ToUInt32(memory.Read(checked(obj + 0x400), 4)) == 61235) vehicles.Add((start + (uint)i, obj)); }
                        catch (IOException) { Missing("scene.scan", "read_failed", "object_read"); }
                    }
                    cachedScene = scene.Value; cachedBegin = begin ?? 0; cachedEnd = end ?? 0;
                    cachedAt = fields.ContainsKey("scene.scan") ? -10000 : nowMs;
                }
            }
            foreach (var (slot, obj) in vehicles)
                if (U32("scene.cached_slot", slot, true) != obj || U32("scene.cached_marker", checked((obj + 0x400)), true) != 61235)
                { Missing("scene.identity", "unstable", "object_replaced"); vehicles.Clear(); break; }
            if (vehicles.Count == 8) Missing("scene.additional_vehicles", "unsupported", "vehicle_limit_8");
            HasVehicle = vehicles.Count > 0; Value("scene.at5t_count", vehicles.Count);
        }
        else { vehicles.Clear(); cachedScene = 0; }
        if (!detailed) return Complete();
        var vtable = U32("actor.vtable", actor.Value, true);
        if (vtable is > 0)
            foreach (var (name, offset) in new[] { ("eligibility", 0xcacu), ("normal_command", 0xe0cu), ("action_set", 0x428u) }) U32("actor.entry." + name, checked(vtable.Value + offset));
        U32("actor.ride", checked(actor.Value + 0x6710)); U8("actor.ride_lock", checked(actor.Value + 0x6970)); Action("actor.action", checked(actor.Value + 0x32d8));
        var descriptor = U32("descriptor.address", checked(actor.Value + 0x3de0), true);
        var input = U32("input.address", checked(actor.Value + 0x643c), true); if (input == 0) input = Global(0x056a3c50);
        if (input is null) Missing("input.fallback", "read_failed", "input_pointer_unavailable");
        else Value("input.fallback", fields["input.address"].Value == 0 ? 1 : 0);
        if (input is > 0) Value("input.resolved_address", input.Value);
        if (descriptor is > 0)
        {
            U8("descriptor.enable", checked(descriptor.Value + 0x30)); U32("descriptor.state", checked(descriptor.Value + 0x2c));
            U32("descriptor.mode", checked(descriptor.Value + 0x28));
            var rules = Vector("rules", descriptor.Value, 4, 8, 28, 32, out var start);
            if (rules is not null)
                for (var i = 0; i < rules.Length / 28; i++)
                {
                    var prefix = "rule." + i; var p = start + (uint)i * 28;
                    var keys = Vector(prefix + ".keys", p, 4, 8, 4, 32, out _);
                    if (keys is not null) for (var j = 0; j < keys.Length / 4; j++) Value(prefix + ".logical." + j, BitConverter.ToUInt32(keys, j * 4));
                    U32(prefix + ".condition_10", checked(p + 0x10)); I32(prefix + ".minimum_duration", checked(p + 0x14)); I32(prefix + ".time_window", checked(p + 0x18));
                }
        }
        if (input is > 0)
        {
            for (uint i = 0; i < 4; i++) U32("input.filter." + i, checked(input.Value + 0x4c) + i * 4);
            U32("input.filter_override", checked(input.Value + 0x84));
            var registered = Vector("input.registered", input.Value, 0x70, 0x74, 8, 4096, out _);
            if (registered is not null && descriptor is > 0)
            {
                var found = false;
                for (var i = 0; i < registered.Length; i += 8) if (BitConverter.ToUInt32(registered, i) == descriptor) { found = true; Value("descriptor.registration_index", i / 8); break; }
                Value("descriptor.registered", found ? 1 : 0);
            }
            else Missing("descriptor.registered", descriptor == 0 ? "not_applicable" : "read_failed", "registration_incomplete");
            var hardware = Vector("input.hardware", input.Value, 0x38, 0x3c, 24, 1024, out _);
            if (hardware is not null)
            {
                var matched = 0;
                for (var i = 0; i < hardware.Length && matched < 32; i += 24)
                    if (BitConverter.ToUInt32(hardware, i) == 4)
                    {
                        var p = "logical4." + matched++; Value(p + ".row_index", i / 24);
                        Value(p + ".time", BitConverter.ToUInt32(hardware, i + 4)); Value(p + ".held_duration", BitConverter.ToUInt32(hardware, i + 8));
                        Value(p + ".down", hardware[i + 12]); Value(p + ".fresh", hardware[i + 13]); Fresh |= hardware[i + 13] != 0;
                    }
                Value("logical4.count", matched);
            }
        }
        var actorPosition = Coordinates("actor", actor.Value);
        var scaleBytes = Bytes("distance.y_scale", Global(0x057c2918), 4, true);
        var scale = scaleBytes is null ? double.NaN : BitConverter.ToSingle(scaleBytes);
        if (double.IsFinite(scale) && scale > 0) Value("distance.y_scale", scale);
        else Missing("distance.y_scale", "read_failed", "invalid_scale");
        for (var i = 0; i < vehicles.Count; i++)
        {
            var (slot, vehicle) = vehicles[i]; var p = "vehicle." + i;
            Value(p + ".address", vehicle); U32(p + ".slot", slot, true); U32(p + ".marker", checked(vehicle + 0x400), true);
            U32(p + ".owner", checked(vehicle + 0x908)); U32(p + ".rider", checked(vehicle + 0x5c84)); U32(p + ".interaction_state", checked(vehicle + 0x1d8));
            U8(p + ".lock", checked(vehicle + 0x5f64)); U32(p + ".mode", checked(vehicle + 0x5f60)); Action(p + ".action", checked(vehicle + 0x32d8));
            var position = Coordinates(p, vehicle);
            if (actorPosition is { } a && position is { } v) {
                var dx = a.X - v.X; var dy = a.Y - v.Y;
                Value(p + ".distance_xy", Math.Sqrt(dx * dx + dy * dy));
                if (double.IsFinite(scale) && scale > 0) Value(p + ".distance_scaled", Math.Sqrt(dx * dx + dy * dy / (scale * scale)));
                else Missing(p + ".distance_scaled", "read_failed", "invalid_scale");
            } else Missing(p + ".distance_xy", "not_applicable", "position_unavailable");
            Missing(p + ".native_distance_result", "unsupported", "polling_does_not_call_game_functions");
        }
        return Complete();
    }
    private Dictionary<string, DiagnosticField> Complete()
    {
        var stable = true;
        foreach (var (p, bytes) in guards)
            try { if (!bytes.AsSpan().SequenceEqual(memory.Read(p, bytes.Length))) stable = false; }
            catch (IOException) { stable = false; }
        stable &= !fields.Values.Any(x => x.Status == "unstable");
        Quality = !stable ? "unstable" : fields.Values.Any(x => x.Status == "read_failed") ? "partial" : "ok";
        if (!stable)
            foreach (var name in fields.Keys.ToArray()) if (fields[name].Status == "ok") fields[name] = new(null, "unstable", "identity_or_protected_field_changed");
        if (!stable) { HasVehicle = false; Fresh = false; }
        return new(fields);
    }
}
