// Package swift generates a standalone Swift client for a Latch protocol.
package swift

import (
	"fmt"
	"strings"

	"github.com/vehmloewff/latch/codegen"
	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

type Options struct{ ClientName string }

func Generate(p *protocol.Protocol, opts Options) (map[string][]byte, error) {
	methodNames := make([]string, len(p.Methods))
	for i, m := range p.Methods {
		methodNames[i] = m.Name
	}
	if err := names.ValidateIdentifiers(methodNames); err != nil {
		return nil, fmt.Errorf("swift: %w", err)
	}
	typeNames, err := names.AssignTypeNames(p.Types)
	if err != nil {
		return nil, fmt.Errorf("swift: %w", err)
	}
	clientName := opts.ClientName
	if clientName == "" {
		clientName = "LatchClient"
	}
	var b strings.Builder
	b.WriteString(codegen.HeaderComment(p.Version))
	b.WriteString(runtime)
	for _, t := range p.Types {
		renderType(&b, t, typeNames)
	}
	eventRef, ok := p.EventRef()
	if !ok {
		return nil, fmt.Errorf("swift: protocol has no event type")
	}
	b.WriteString("\npublic enum ConnectionState: String, Sendable { case connecting, offline, connected }\n\n")
	fmt.Fprintf(&b, "public final class %s {\n  private let url: URL\n  private let onEvent: @Sendable (%s) -> Void\n  private let onConnectionStateChange: (@Sendable (ConnectionState) -> Void)?\n  public init(url: URL, onEvent: @escaping @Sendable (%s) -> Void, onConnectionStateChange: (@Sendable (ConnectionState) -> Void)? = nil) { self.url = url; self.onEvent = onEvent; self.onConnectionStateChange = onConnectionStateChange }\n  public func connect() async throws -> Connected%s {\n    onConnectionStateChange?(.connecting)\n    do { return try await Connected%s.connect(url: url, version: %q, onEvent: onEvent, onConnectionStateChange: onConnectionStateChange) }\n    catch { onConnectionStateChange?(.offline); throw error }\n  }\n}\n\n", clientName, swiftType(eventRef, typeNames), swiftType(eventRef, typeNames), clientName, clientName, p.Version)
	fmt.Fprintf(&b, "public final class Connected%s {\n  private let transport: LatchTransport\n  private init(transport: LatchTransport, onEvent: @escaping @Sendable (%s) -> Void, onConnectionStateChange: (@Sendable (ConnectionState) -> Void)?) {\n    self.transport = transport\n    onConnectionStateChange?(.connected)\n    Task {\n      for await data in transport.events {\n        if let value = try? LatchBinary.decode(data, as: %s.self) { onEvent(value) }\n      }\n      onConnectionStateChange?(.offline)\n    }\n  }\n  fileprivate static func connect(url: URL, version: String, onEvent: @escaping @Sendable (%s) -> Void, onConnectionStateChange: (@Sendable (ConnectionState) -> Void)?) async throws -> Connected%s { let t = try await LatchTransport.connect(url: url, version: version); return Connected%s(transport: t, onEvent: onEvent, onConnectionStateChange: onConnectionStateChange) }\n", clientName, swiftType(eventRef, typeNames), swiftType(eventRef, typeNames), swiftType(eventRef, typeNames), clientName, clientName)
	for _, m := range p.Methods {
		fmt.Fprintf(&b, "  public func %s(_ request: %s) async throws -> %s { try await transport.call(%q, request: request) }\n", names.CamelCase(m.Name), swiftType(m.RequestType, typeNames), swiftType(m.ResponseType, typeNames), m.Name)
	}
	b.WriteString("  public func close() async { await transport.close() }\n}\n")
	return map[string][]byte{"LatchClient.swift": []byte(b.String())}, nil
}

func swiftType(r protocol.TypeRef, namesByID map[string]string) string {
	switch r.Kind {
	case protocol.KindString:
		return "String"
	case protocol.KindBool:
		return "Bool"
	case protocol.KindInt, protocol.KindInt64:
		return "Int64"
	case protocol.KindInt8:
		return "Int8"
	case protocol.KindInt16:
		return "Int16"
	case protocol.KindInt32:
		return "Int32"
	case protocol.KindUint, protocol.KindUint64:
		return "UInt64"
	case protocol.KindUint8:
		return "UInt8"
	case protocol.KindUint16:
		return "UInt16"
	case protocol.KindUint32:
		return "UInt32"
	case protocol.KindFloat32:
		return "Float"
	case protocol.KindFloat64:
		return "Double"
	case protocol.KindTime:
		return "Date"
	case protocol.KindPointer:
		return swiftType(*r.Elem, namesByID) + "?"
	case protocol.KindSlice, protocol.KindArray:
		return "[" + swiftType(*r.Elem, namesByID) + "]"
	case protocol.KindMap:
		return "[String: " + swiftType(*r.MapValue, namesByID) + "]"
	case protocol.KindStruct, protocol.KindEnum:
		return namesByID[r.NamedType]
	default:
		return "Never"
	}
}

func swiftDecodeExpr(ref protocol.TypeRef, value string, typeNames map[string]string) string {
	switch ref.Kind {
	case protocol.KindSlice, protocol.KindArray:
		return fmt.Sprintf("LatchValue.decodeList(%s) { item in try %s }", value, swiftDecodeExpr(*ref.Elem, "item", typeNames))
	case protocol.KindMap:
		return fmt.Sprintf("LatchValue.decodeMap(%s) { item in try %s }", value, swiftDecodeExpr(*ref.MapValue, "item", typeNames))
	case protocol.KindPointer:
		return swiftDecodeExpr(*ref.Elem, value, typeNames)
	default:
		return fmt.Sprintf("LatchValue.convert(%s, to: %s.self)", value, swiftType(ref, typeNames))
	}
}

func renderType(b *strings.Builder, t *protocol.NamedType, typeNames map[string]string) {
	name := typeNames[t.ID]
	if t.Kind == protocol.KindEnum {
		fmt.Fprintf(b, "\npublic enum %s: String, Sendable, LatchCodable {\n", name)
		for _, v := range t.EnumValues {
			fmt.Fprintf(b, "  case %s = %q\n", names.PascalCase(v), v)
		}
		b.WriteString("  var latchValue: LatchValue { .string(rawValue) }\n  static func decodeLatch(_ value: LatchValue) throws -> Self { guard case let .string(s) = value, let v = Self(rawValue: s) else { throw LatchError.malformed }; return v }\n}\n")
		return
	}
	fmt.Fprintf(b, "\npublic struct %s: Sendable, LatchCodable {\n", name)
	for _, f := range t.Fields {
		field := names.CamelCase(f.GoName)
		ty := swiftType(f.Type, typeNames)
		if (f.Nullable || f.Optional) && !strings.HasSuffix(ty, "?") {
			ty += "?"
		}
		fmt.Fprintf(b, "  public var %s: %s\n", field, ty)
	}
	b.WriteString("  public init(")
	for i, f := range t.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		ty := swiftType(f.Type, typeNames)
		if (f.Nullable || f.Optional) && !strings.HasSuffix(ty, "?") {
			ty += "?"
		}
		fmt.Fprintf(b, "%s: %s%s", names.CamelCase(f.GoName), ty, func() string {
			if f.Nullable || f.Optional {
				return " = nil"
			}
			return ""
		}())
	}
	b.WriteString(") {\n")
	for _, f := range t.Fields {
		field := names.CamelCase(f.GoName)
		fmt.Fprintf(b, "    self.%s = %s\n", field, field)
	}
	b.WriteString("  }\n")
	if len(t.Fields) == 0 {
		b.WriteString("  var latchValue: LatchValue { .structure([:]) }\n  static func decodeLatch(_ latchValue: LatchValue) throws -> Self { guard case .structure = latchValue else { throw LatchError.malformed }; return Self() }\n}\n")
		return
	}
	b.WriteString("  var latchValue: LatchValue { .structure([")
	for i, f := range t.Fields {
		if i > 0 {
			b.WriteString(",")
		}
		field := names.CamelCase(f.GoName)
		num := i + 1
		if f.Nullable || f.Optional {
			fmt.Fprintf(b, "%d: %s.map(LatchValue.from) ?? .null", num, field)
		} else {
			fmt.Fprintf(b, "%d: LatchValue.from(%s)", num, field)
		}
	}
	b.WriteString("]) }\n  static func decodeLatch(_ latchValue: LatchValue) throws -> Self { guard case let .structure(fields) = latchValue else { throw LatchError.malformed };\n")
	for i, f := range t.Fields {
		field := names.CamelCase(f.GoName)
		ty := swiftType(f.Type, typeNames)
		num := i + 1
		if f.Nullable || f.Optional {
			decodeType := f.Type
			if decodeType.Kind == protocol.KindPointer {
				decodeType = *decodeType.Elem
			}
			fmt.Fprintf(b, "    let %s: %s = try LatchValue.optional(fields[%d]) { value in try %s }\n", field, ty, num, swiftDecodeExpr(decodeType, "value", typeNames))
		} else {
			fmt.Fprintf(b, "    guard let v%d = fields[%d] else { throw LatchError.malformed }; let %s = try %s\n", i, num, field, swiftDecodeExpr(f.Type, fmt.Sprintf("v%d", i), typeNames))
		}
	}
	b.WriteString("    return Self(")
	for i, f := range t.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		field := names.CamelCase(f.GoName)
		fmt.Fprintf(b, "%s: %s", field, field)
	}
	b.WriteString(")\n  }\n}\n")
}

const runtime = `
import Foundation

public struct LatchError: Error, Sendable { public let code: String; public let message: String; static let malformed = LatchError(code: "malformed_frame", message: "malformed binary frame") }
protocol LatchCodable { var latchValue: LatchValue { get }; static func decodeLatch(_ value: LatchValue) throws -> Self }
indirect enum LatchValue: Equatable { case null, bool(Bool), int(Int64), uint(UInt64), float32(Float), float64(Double), string(String), time(Date), list([LatchValue]), map([String: LatchValue]), structure([Int: LatchValue])
 static func from<T>(_ x: T) -> LatchValue { if let x=x as? any LatchCodable{return x.latchValue}; if let x=x as? String{return .string(x)}; if let x=x as? Bool{return .bool(x)}; if let x=x as? Int8{return .int(Int64(x))}; if let x=x as? Int16{return .int(Int64(x))}; if let x=x as? Int32{return .int(Int64(x))}; if let x=x as? Int64{return .int(x)}; if let x=x as? Int{return .int(Int64(x))}; if let x=x as? UInt8{return .uint(UInt64(x))}; if let x=x as? UInt16{return .uint(UInt64(x))}; if let x=x as? UInt32{return .uint(UInt64(x))}; if let x=x as? UInt64{return .uint(x)}; if let x=x as? Float{return .float32(x)}; if let x=x as? Double{return .float64(x)}; if let x=x as? Date{return .time(x)}; if let x=x as? [LatchValue]{return .list(x)}; if let x=x as? [String: LatchValue]{return .map(x)}; let mirror=Mirror(reflecting:x); if mirror.displayStyle == .collection{return .list(mirror.children.map{from($0.value)})}; if mirror.displayStyle == .dictionary{var m:[String:LatchValue]=[:];for e in mirror.children{let pair=Array(Mirror(reflecting:e.value).children);if pair.count==2,let k=pair[0].value as? String{m[k]=from(pair[1].value)}};return .map(m)}; return .null }
 static func optional<T>(_ v: LatchValue?, transform:(LatchValue)throws->T) throws -> T? { guard let v else{return nil}; if case .null = v{return nil}; return try transform(v) }; static func decodeList<T>(_ v:LatchValue, transform:(LatchValue)throws->T)throws->[T]{guard case let .list(xs)=v else{throw LatchError.malformed};return try xs.map(transform)}; static func decodeMap<T>(_ v:LatchValue, transform:(LatchValue)throws->T)throws->[String:T]{guard case let .map(xs)=v else{throw LatchError.malformed};var out:[String:T]=[:];for (k,item) in xs{out[k]=try transform(item)};return out}; static func convert<T>(_ v: LatchValue, to: T.Type) throws -> T { if let x = v as? T { return x }; switch v { case .string(let s): if T.self == String.self{return s as! T}; if let t=T.self as? any LatchCodable.Type, let x=try? t.decodeLatch(v) as? T{return x}; if T.self == LatchValue.self{return v as! T}; case .bool(let x): if T.self == Bool.self{return x as! T}; case .int(let x): if let y = T.self as? any FixedWidthInteger.Type, let n=y.init(exactly:x){return n as! T}; case .uint(let x): if let y = T.self as? any FixedWidthInteger.Type, let n=y.init(exactly:x){return n as! T}; case .float32(let x): if T.self == Float.self{return x as! T}; case .float64(let x): if T.self == Double.self{return x as! T}; case .time(let x): if T.self == Date.self{return x as! T}; case .list(let xs): if T.self == [LatchValue].self{return xs as! T}; if let y = try? xs.map({ try convert($0,to: (T.self as? [LatchValue].Type)?.Element.self ?? LatchValue.self) }) as? T{return y}; case .map(let x): if T.self == [String: LatchValue].self{return x as! T}; case .structure: if let t=T.self as? any LatchCodable.Type, let x=try? t.decodeLatch(v) as? T{return x}; case .null: break }; throw LatchError.malformed }
}
private struct LatchDecoder: Decoder { let value: LatchValue; init(_ v: LatchValue){value=v}; var codingPath:[any CodingKey]=[]; var userInfo:[CodingUserInfoKey:Any]=[:]; func container<Key>(keyedBy:Key.Type)throws->KeyedDecodingContainer<Key> where Key:CodingKey { throw LatchError.malformed }; func unkeyedContainer()throws->any UnkeyedDecodingContainer { throw LatchError.malformed }; func singleValueContainer()throws->any SingleValueDecodingContainer { throw LatchError.malformed } }
enum LatchBinary { static func encode<T: LatchCodable>(_ value:T)throws->Data { var w=Writer();w.value(value.latchValue);return w.b }; static func decode<T: LatchCodable>(_ data:Data,as:T.Type)throws->T { var r=Reader(data); let v=try r.value(); guard r.done() else { throw LatchError.malformed }; return try T.decodeLatch(v) }; static func encodeValue(_ value:LatchValue)throws->Data { var w=Writer();w.value(value);return w.b }; static func decodeValue(_ data:Data)throws->LatchValue { var r=Reader(data);let value=try r.value();guard r.done() else{throw LatchError.malformed};return value } }
private struct Writer { var b=Data(); mutating func byte(_ x:UInt8){b.append(x)}; mutating func u(_ x:UInt64){var n=x; repeat {var c=UInt8(n&127); n >>= 7; if n != 0 {c |= 128}; byte(c)} while n != 0}; mutating func blob(_ d:Data){u(UInt64(d.count));b.append(d)}; mutating func value(_ v:LatchValue){switch v {case .null:byte(0);case .bool(let x):byte(x ? 2:1);case .int(let x):byte(3);u((UInt64(bitPattern:x)<<1) ^ UInt64(bitPattern:x>>63));case .uint(let x):byte(4);u(x);case .float32(let x):byte(5);var n=x.bitPattern.littleEndian;b.append(Data(bytes:&n,count:4));case .float64(let x):byte(6);var n=x.bitPattern.littleEndian;b.append(Data(bytes:&n,count:8));case .string(let x):byte(7);blob(Data(x.utf8));case .time(let x):byte(12);let n=Int64(x.timeIntervalSince1970*1_000_000_000);u((UInt64(bitPattern:n)<<1) ^ UInt64(bitPattern:n>>63));case .list(let a):byte(10);u(UInt64(a.count));a.forEach{value($0)};case .map(let m):byte(11);u(UInt64(m.count));for k in m.keys.sorted(){value(.string(k));value(m[k]!)};case .structure(let m):byte(9);u(UInt64(m.count));for k in m.keys.sorted(){u(UInt64(k));value(m[k]!)} }} }
private struct Reader { let d:Data; var p=0; init(_ d:Data){self.d=d}; mutating func byte()throws->UInt8{guard p<d.count else{throw LatchError.malformed};defer{p+=1};return d[p]}; mutating func u()throws->UInt64{var x:UInt64=0;for i in 0..<10{let c=try byte();if i==9 && (c&0xfe) != 0{throw LatchError.malformed};x |= UInt64(c&127)<<UInt64(i*7);if c&128==0{if i>0 && (c&127)==0{throw LatchError.malformed};return x}};throw LatchError.malformed}; mutating func count()throws->Int{let n=try u();guard n <= 1<<24 else{throw LatchError.malformed};return Int(n)}; mutating func blob()throws->Data{let n=try count();return try raw(n)}; mutating func raw(_ n:Int)throws->Data{guard n>=0&&n<=d.count-p else{throw LatchError.malformed};defer{p+=n};return d.subdata(in:p..<p+n)}; func done()->Bool{p==d.count}; mutating func value()throws->LatchValue{switch try byte(){case 0:return .null;case 1:return .bool(false);case 2:return .bool(true);case 3:let n=try u();return .int(Int64(bitPattern:(n>>1) ^ (0 &- (n&1))));case 4:return .uint(try u());case 5:let x=try raw(4);return .float32(Float(bitPattern:UInt32(littleEndian:x.withUnsafeBytes{$0.loadUnaligned(as:UInt32.self)})));case 6:let x=try raw(8);return .float64(Double(bitPattern:UInt64(littleEndian:x.withUnsafeBytes{$0.loadUnaligned(as:UInt64.self)})));case 7:let bytes=try blob();guard let s=String(data:bytes,encoding:.utf8) else{throw LatchError.malformed};return .string(s);case 9:let n=try count();var m:[Int:LatchValue]=[:];for _ in 0..<n{m[Int(try u())]=try value()};return .structure(m);case 10:let n=try count();return .list(try (0..<n).map{_ in try value()});case 11:let n=try count();var m:[String:LatchValue]=[:];for _ in 0..<n{guard case let .string(k)=try value() else{throw LatchError.malformed};m[k]=try value()};return .map(m);case 12:let z=try u();let n=Int64(bitPattern:(z>>1) ^ (0 &- (z&1)));return .time(Date(timeIntervalSince1970:Double(n)/1e9));default:throw LatchError.malformed}} }
private struct Envelope { var kind:UInt8; var version=""; var id=""; var method=""; var event=""; var payload=Data(); var error=""; var code=""; func data()->Data{var w=Writer();w.byte(1);w.byte(kind);[version,id,method,event].forEach{w.blob(Data($0.utf8))};w.blob(payload);w.blob(Data(error.utf8));w.blob(Data(code.utf8));return w.b}; static func parse(_ d:Data)throws->Envelope{var r=Reader(d);guard try r.byte()==1 else{throw LatchError.malformed};var e=Envelope(kind:try r.byte());func s()throws->String{guard let value=String(data:try r.blob(),encoding:.utf8) else{throw LatchError.malformed};return value};e.version=try s();e.id=try s();e.method=try s();e.event=try s();e.payload=try r.blob();e.error=try s();e.code=try s();guard r.done() else{throw LatchError.malformed};return e} }
private actor LatchTransport { let ws:URLSessionWebSocketTask; var next=1; var pending:[String:CheckedContinuation<Data,Error>]=[:]; var closed=false; nonisolated let events:AsyncStream<Data>; let cont:AsyncStream<Data>.Continuation; private init(_ ws:URLSessionWebSocketTask){self.ws=ws;var c:AsyncStream<Data>.Continuation!;events=AsyncStream{c=$0};cont=c;Task{await self.receive()}}; static func connect(url:URL,version:String)async throws->LatchTransport{guard var components=URLComponents(url:url,resolvingAgainstBaseURL:false) else{throw LatchError.malformed};var query=components.queryItems ?? [];query.removeAll{$0.name == "version"};query.append(URLQueryItem(name:"version",value:version));components.queryItems=query;guard let target=components.url else{throw LatchError.malformed};let ws=URLSession.shared.webSocketTask(with:target);ws.resume();do{try await withCheckedThrowingContinuation{(c:CheckedContinuation<Void,Error>) in ws.sendPing{error in if let error{c.resume(throwing:error)}else{c.resume()}}}}catch{ws.cancel(with:.goingAway,reason:nil);throw error};return LatchTransport(ws)}; func call<Req:LatchCodable,Resp:LatchCodable>(_ method:String,request:Req)async throws->Resp{guard !closed else{throw LatchError(code:"connection_closed",message:"the connection is closed")};let id=String(next);next+=1;let payload=try LatchBinary.encode(request);let bytes=Envelope(kind:3,id:id,method:method,payload:payload).data();let response=try await withCheckedThrowingContinuation{(c:CheckedContinuation<Data,Error>) in pending[id]=c;Task{do{try await ws.send(.data(bytes))}catch{self.reject(id,error)}}};return try LatchBinary.decode(response,as:Resp.self)}; func reject(_ id:String,_ e:Error){pending.removeValue(forKey:id)?.resume(throwing:e)}; func receive()async{while true{do{let m=try await ws.receive();guard case let .data(d)=m else{continue};let e=try Envelope.parse(d);if e.kind==4{pending.removeValue(forKey:e.id)?.resume(returning:e.payload)}else if e.kind==5{pending.removeValue(forKey:e.id)?.resume(throwing:LatchError(code:e.code,message:e.error))}else if e.kind==6{cont.yield(e.payload)}else if e.kind==7{throw LatchError(code:e.code,message:e.error)}}catch{closed=true;for (_,c) in pending{c.resume(throwing:error)};pending.removeAll();cont.finish();return}}}; func close()async{guard !closed else{return};closed=true;ws.cancel(with:.normalClosure,reason:nil);let error=LatchError(code:"connection_closed",message:"the connection is closed");for (_,c) in pending{c.resume(throwing:error)};pending.removeAll();cont.finish()} }
`
