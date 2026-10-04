#!/usr/bin/env swift
// Reuses the chevron and dashboard-grid geometry/colors from the checked-in
// logo-dark-dashboard.svg. Only the square icon layout is new; no source edit.
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

guard CommandLine.arguments.count == 2 else { fatalError("Supply an output PNG path") }
let context = CGContext(data: nil, width: 1024, height: 1024, bitsPerComponent: 8,
    bytesPerRow: 4096, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
context.setFillColor(CGColor(red: 23/255, green: 23/255, blue: 29/255, alpha: 1))
context.fill(CGRect(x: 0, y: 0, width: 1024, height: 1024))
// SVG coordinates have a downward y-axis.
context.translateBy(x: 0, y: 1024); context.scaleBy(x: 1, y: -1)
context.saveGState()
context.translateBy(x: 174, y: 322); context.scaleBy(x: 6.2, y: 6.2)
context.translateBy(x: -17.4, y: -21.5)
context.setFillColor(CGColor(red: 115/255, green: 83/255, blue: 237/255, alpha: 1))
for points: [CGPoint] in [
    [.init(x:17.4,y:27.9), .init(x:23.8,y:21.5), .init(x:54.3,y:52), .init(x:47.9,y:58.4)],
    [.init(x:23.8,y:82.4), .init(x:17.5,y:76.1), .init(x:48,y:45.6), .init(x:54.3,y:51.9)]
] {
    context.beginPath(); context.addLines(between: points); context.closePath(); context.fillPath()
}
context.restoreGState()
context.saveGState()
context.translateBy(x: 480, y: 385); context.scaleBy(x: 12, y: 12)
context.translateBy(x: -175.8, y: -93.9)
context.setStrokeColor(CGColor(red: 87/255, green: 211/255, blue: 140/255, alpha: 1))
context.setLineWidth(1.5)
// The two rotated rectangles are expressed in their final SVG coordinates.
for rect in [CGRect(x:175.8,y:93.9,width:9.1,height:20.7),
             CGRect(x:188.1,y:93.8,width:20.7,height:9.1),
             CGRect(x:188.0,y:105.5,width:20.7,height:9.1)] {
    context.addPath(CGPath(roundedRect: rect, cornerWidth: 2.2, cornerHeight: 2.2, transform: nil)); context.strokePath()
}
context.restoreGState()
let image = context.makeImage()!
let destination = CGImageDestinationCreateWithURL(URL(fileURLWithPath: CommandLine.arguments[1]) as CFURL, UTType.png.identifier as CFString, 1, nil)!
CGImageDestinationAddImage(destination, image, nil)
guard CGImageDestinationFinalize(destination) else { fatalError("PNG encoding failed") }
